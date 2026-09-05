package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// ClientVersionHeader carries the caller's client-library version on every
// request. A deployment states the oldest client it serves and refuses anything
// below it with 426, so a stale binary is diagnosed by name instead of failing
// somewhere downstream as a request the API cannot parse.
const ClientVersionHeader = "X-Fpcloud-Client-Version"

// Client is the Fogpipe API client.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	// Version is what this caller reports as its client-library version. It
	// defaults to the version of this module recorded in the caller's build
	// info, which is right for anything importing pkg/client as a dependency.
	// The CLI ships as this module's own main package, where build info carries
	// no tag, and sets its ldflags-injected version instead.
	Version string
}

// Bounds on getting to a first byte. Each one names a phase that can be waited
// on without knowing anything about the response, so each can fail with a real
// error the caller can read.
//
// There is deliberately no bound on the whole exchange. `http.Client.Timeout`
// is a total deadline that covers reading the body, so it cannot tell a
// response that is long from a server that is stuck: it cut `app logs --follow`
// at 30 seconds, where ADR-086 promises thirty minutes, and it would do the
// same to any future streamed or slow answer. That is the client-side shape of
// what ADR-099 removed from the server, and it is removed here for the same
// reason — a bound that cannot answer is not a bound.
//
// What remains: ResponseHeaderTimeout bounds the case the total deadline was
// really guarding, a server that never answers at all, and the caller's context
// ends anything longer. A response that has begun is bounded at the edge, which
// answers 504.
const (
	dialTimeout           = 10 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	responseHeaderTimeout = 30 * time.Second
)

// New creates a new API client.
func New(baseURL, apiKey string) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: dialTimeout}).DialContext
	transport.TLSHandshakeTimeout = tlsHandshakeTimeout
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Transport: transport,
		},
		Version: moduleVersion(),
	}
}

// modulePath is this module — the import path the version is read back out of.
const modulePath = "github.com/fogpipe/cloud-cli"

// moduleVersion reports the version of this module the caller was built
// against, or "" when the build carries no version (a `go run`, a workspace
// build, or this module's own binaries). An empty version sends no header,
// which a deployment treats as an unidentified caller rather than as an old
// one: claiming a version we cannot substantiate is worse than claiming none.
func moduleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, dep := range info.Deps {
		if dep.Path == modulePath {
			if dep.Replace != nil {
				return ""
			}
			return dep.Version
		}
	}
	return ""
}

func (c *Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.Version != "" {
		req.Header.Set(ClientVersionHeader, c.Version)
	}

	return req, nil
}

// maxRateLimitRetries bounds how many times a refused request is repeated. The
// platform refuses a rate-limited request BEFORE it reaches the handler, so no
// work happened and nothing was half-created — the request is repeatable
// whatever its method, which is what makes retrying a POST sound here rather
// than a hack. What it is not is unbounded: a caller that keeps retrying into a
// budget it has genuinely exhausted is the load it is being refused for.
const maxRateLimitRetries = 4

// maxRetryAfter caps how long a single refusal may park the caller, however
// long the server asks for. The caller's own context still ends anything
// longer, and is the real bound (ADR-107).
const maxRetryAfter = 60 * time.Second

func (c *Client) do(req *http.Request, out any) error {
	for attempt := 0; ; attempt++ {
		retryIn, err := c.attempt(req, out)
		if retryIn <= 0 || attempt >= maxRateLimitRetries {
			return err
		}
		replay, replayErr := replayRequest(req)
		if replayErr != nil {
			return err
		}
		if waitErr := wait(req.Context(), retryIn); waitErr != nil {
			return waitErr
		}
		req = replay
	}
}

// attempt performs one exchange. A non-zero duration means the platform refused
// the request as rate limited and said when to come back; the error is returned
// alongside it, so a caller that stops retrying still reports what happened.
func (c *Client) attempt(req *http.Request, out any) (time.Duration, error) {
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return retryAfter(resp.Header.Get("Retry-After")), responseError(resp)
	}
	if resp.StatusCode >= 400 {
		return 0, responseError(resp)
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return 0, fmt.Errorf("decoding response: %w", err)
		}
	}

	return 0, nil
}

// retryAfter reads how long the platform asked us to wait, and spreads the
// answer. Every client of one organization shares one budget, so they are
// refused together and told the same second to return — retrying at exactly
// that moment recreates the burst that was refused. The jitter is added, never
// subtracted: coming back early is another refusal.
func retryAfter(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds < 0 {
		// No usable header. Wait a second rather than not retrying at all: a
		// refusal we cannot read is still a refusal, and the attempt count is
		// what bounds us.
		seconds = 1
	}
	base := time.Duration(seconds) * time.Second
	if base > maxRetryAfter {
		base = maxRetryAfter
	}
	if base <= 0 {
		base = time.Second
	}
	return base + time.Duration(rand.Int63n(int64(base/2)+1))
}

// wait sleeps, unless the caller's context ends first — a retry must never
// outlive the deadline the caller set (ADR-107).
func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// replayRequest rebuilds a request for a second attempt. A body built by
// newRequest is a *bytes.Reader, so net/http records a GetBody that hands back a
// fresh copy of it; a request whose body cannot be rebuilt is not replayed.
func replayRequest(req *http.Request) (*http.Request, error) {
	replay := req.Clone(req.Context())
	if req.GetBody == nil {
		return replay, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	replay.Body = body
	return replay, nil
}

// responseError turns an error response into an APIError, falling back to the
// bare status when the body is not the API's error shape.
func responseError(resp *http.Response) error {
	apiErr := &APIError{StatusCode: resp.StatusCode}
	if err := json.NewDecoder(resp.Body).Decode(apiErr); err != nil {
		return &APIError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	return apiErr
}

// FKECredentials fetches the cluster connection facts for a kubeconfig context
// scoped to the project (GET /projects/{id}/fke/credentials). Returns an error
// matching client.ErrNotFound when the API predates the endpoint (404), letting
// the CLI fall back to embedded constants.
func (c *Client) FKECredentials(ctx context.Context, projectID string) (*ClusterCredentials, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/fke/credentials", nil)
	if err != nil {
		return nil, err
	}
	var creds ClusterCredentials
	if err := c.do(httpReq, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

// ClusterInfo fetches the project-independent cluster connection facts (apiserver
// URL + CA) for assembling a cluster-admin kubeconfig — the staff FKE path, which
// is not project-scoped.
func (c *Client) ClusterInfo(ctx context.Context) (*ClusterInfo, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/cluster-info", nil)
	if err != nil {
		return nil, err
	}
	var info ClusterInfo
	if err := c.do(httpReq, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// FKEToken mints a short-lived, namespace-scoped Kubernetes token bound to the
// project's ServiceAccount (POST /projects/{id}/fke/token). kubectl's exec plugin
// calls this transparently.
func (c *Client) FKEToken(ctx context.Context, projectID string) (*ClusterToken, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/projects/"+projectID+"/fke/token", nil)
	if err != nil {
		return nil, err
	}
	var tok ClusterToken
	if err := c.do(httpReq, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

// AuthConfig reports where humans sign in (ADR-132): the identity provider, its
// endpoints, the client ids the platform registered for the CLI and the
// console, and whether the CLI's token exchange goes through the platform.
// Unauthenticated; asked before a credential exists.
func (c *Client) AuthConfig(ctx context.Context) (*AuthConfigResponse, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/auth/config", nil)
	if err != nil {
		return nil, err
	}
	var resp AuthConfigResponse
	if err := c.do(httpReq, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetMe retrieves the current user's info.
// AuthSecurity reports how the signed-in person signs in: their factors at
// the identity provider, and where to manage them.
func (c *Client) AuthSecurity(ctx context.Context) (*AuthSecurityResponse, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/auth/security", nil)
	if err != nil {
		return nil, err
	}
	var resp AuthSecurityResponse
	if err := c.do(httpReq, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetMe(ctx context.Context) (*MeResponse, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/auth/me", nil)
	if err != nil {
		return nil, err
	}
	var resp MeResponse
	if err := c.do(httpReq, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RegistryRepository is one image repository visible to a project, with the
// <org_short_id>/<project>/ prefix stripped for display.
type RegistryRepository struct {
	Name string `json:"name"`
}

// RegistryTagList is the set of image tags for one repository.
type RegistryTagList struct {
	Repository string   `json:"repository"`
	Tags       []string `json:"tags"`
}

// RegistryVulnerabilities is a CVE severity roll-up for one image, from zot's
// search-extension Trivy scanner. Nil/absent when CVE scanning is not enabled.
type RegistryVulnerabilities struct {
	MaxSeverity string `json:"max_severity"`
	Total       int    `json:"total"`
	Critical    int    `json:"critical"`
	High        int    `json:"high"`
	Medium      int    `json:"medium"`
	Low         int    `json:"low"`
	Unknown     int    `json:"unknown"`
}

// RegistryImage is one tagged image with metadata from the zot search extension.
// Size/Digest are zero when the search extension is unavailable.
//
// FirstSeenAt is when the registry was first seen holding this manifest, which
// fpcloud records itself. It is not the image's build date: the only timestamp
// an OCI image carries is the one its builder wrote, and reproducible builds pin
// that to a fixed epoch. Nil means no record yet, not old.
type RegistryImage struct {
	Tag             string                   `json:"tag"`
	Digest          string                   `json:"digest,omitempty"`
	Size            int64                    `json:"size,omitempty"`
	FirstSeenAt     *time.Time               `json:"first_seen_at,omitempty"`
	Vulnerabilities *RegistryVulnerabilities `json:"vulnerabilities,omitempty"`
}

// RegistryImageList is the enriched set of images for one repository.
type RegistryImageList struct {
	Repository string          `json:"repository"`
	Images     []RegistryImage `json:"images"`
}

// RegistryRetentionPolicy is an auto-delete rule for a project's registry repos.
// An empty Repo is the project-wide default. KeepLast keeps the newest N tags;
// MaxAgeDays deletes tags older than N days (newest KeepLast always protected).
type RegistryRetentionPolicy struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"project_id"`
	Repo       string    `json:"repo"`
	KeepLast   int       `json:"keep_last"`
	MaxAgeDays int       `json:"max_age_days"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// SetRetentionPolicyRequest upserts a retention policy for (project, repo).
type SetRetentionPolicyRequest struct {
	Repo       string `json:"repo"`
	KeepLast   int    `json:"keep_last"`
	MaxAgeDays int    `json:"max_age_days"`
	Enabled    bool   `json:"enabled"`
}

// RegistryRepoVisibility is a per-repository public/private setting (ADR-013 S4).
// A repo with Public = true is anonymously pullable; absence of a record means
// private. Repo is the project-relative name.
type RegistryRepoVisibility struct {
	ProjectID string    `json:"project_id"`
	Repo      string    `json:"repo"`
	Public    bool      `json:"public"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SetRegistryVisibilityRequest upserts a repository's visibility for (project, repo).
type SetRegistryVisibilityRequest struct {
	Repo   string `json:"repo"`
	Public bool   `json:"public"`
}

// RetentionPreviewItem is one tag a retention policy would delete.
type RetentionPreviewItem struct {
	Repo        string     `json:"repo"`
	Tag         string     `json:"tag"`
	Digest      string     `json:"digest,omitempty"`
	Reason      string     `json:"reason"`
	FirstSeenAt *time.Time `json:"first_seen_at,omitempty"`
}

// RetentionPreview is the dry-run (or applied) set of retention deletions.
type RetentionPreview struct {
	Items []RetentionPreviewItem `json:"items"`
}

// ListRegistryRepositories lists a project's registry repositories.
func (c *Client) ListRegistryRepositories(ctx context.Context, projectID string) ([]RegistryRepository, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/repositories", nil)
	if err != nil {
		return nil, err
	}
	var repos []RegistryRepository
	if err := c.do(httpReq, &repos); err != nil {
		return nil, err
	}
	return repos, nil
}

// ListRegistryTags lists the tags of one repository (project-relative name).
func (c *Client) ListRegistryTags(ctx context.Context, projectID, repo string) (*RegistryTagList, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet,
		"/api/v1/projects/"+projectID+"/repositories/tags?repo="+url.QueryEscape(repo), nil)
	if err != nil {
		return nil, err
	}
	var tags RegistryTagList
	if err := c.do(httpReq, &tags); err != nil {
		return nil, err
	}
	return &tags, nil
}

// DeleteRegistryTag deletes one tag from a repository (project-relative name).
func (c *Client) DeleteRegistryTag(ctx context.Context, projectID, repo, tag string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete,
		"/api/v1/projects/"+projectID+"/repositories/tags?repo="+url.QueryEscape(repo)+"&tag="+url.QueryEscape(tag), nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// ListRetentionPolicies lists a project's registry retention policies.
func (c *Client) ListRetentionPolicies(ctx context.Context, projectID string) ([]*RegistryRetentionPolicy, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/repositories/retention", nil)
	if err != nil {
		return nil, err
	}
	var policies []*RegistryRetentionPolicy
	if err := c.do(httpReq, &policies); err != nil {
		return nil, err
	}
	return policies, nil
}

// SetRetentionPolicy upserts a retention policy (empty Repo = project default).
func (c *Client) SetRetentionPolicy(ctx context.Context, projectID string, req SetRetentionPolicyRequest) (*RegistryRetentionPolicy, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/projects/"+projectID+"/repositories/retention", req)
	if err != nil {
		return nil, err
	}
	var policy RegistryRetentionPolicy
	if err := c.do(httpReq, &policy); err != nil {
		return nil, err
	}
	return &policy, nil
}

// DeleteRetentionPolicy removes a retention policy (empty repo = project default).
func (c *Client) DeleteRetentionPolicy(ctx context.Context, projectID, repo string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete,
		"/api/v1/projects/"+projectID+"/repositories/retention?repo="+url.QueryEscape(repo), nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// PreviewRetention dry-runs the project's retention policies, returning the
// tags they would delete right now.
func (c *Client) PreviewRetention(ctx context.Context, projectID string) (*RetentionPreview, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/projects/"+projectID+"/repositories/retention/preview", nil)
	if err != nil {
		return nil, err
	}
	var preview RetentionPreview
	if err := c.do(httpReq, &preview); err != nil {
		return nil, err
	}
	return &preview, nil
}

// ApplyRetention enforces the project's retention policies now, deleting the
// selected tags and returning them.
func (c *Client) ApplyRetention(ctx context.Context, projectID string) (*RetentionPreview, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/projects/"+projectID+"/repositories/retention/apply", nil)
	if err != nil {
		return nil, err
	}
	var preview RetentionPreview
	if err := c.do(httpReq, &preview); err != nil {
		return nil, err
	}
	return &preview, nil
}

// ListRegistryVisibility lists a project's per-repo visibility records. A repo
// with no record is private (the default).
func (c *Client) ListRegistryVisibility(ctx context.Context, projectID string) ([]*RegistryRepoVisibility, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/repositories/visibility", nil)
	if err != nil {
		return nil, err
	}
	var records []*RegistryRepoVisibility
	if err := c.do(httpReq, &records); err != nil {
		return nil, err
	}
	return records, nil
}

// SetRegistryVisibility sets one repository's public/private visibility.
func (c *Client) SetRegistryVisibility(ctx context.Context, projectID string, req SetRegistryVisibilityRequest) (*RegistryRepoVisibility, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/projects/"+projectID+"/repositories/visibility", req)
	if err != nil {
		return nil, err
	}
	var record RegistryRepoVisibility
	if err := c.do(httpReq, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// CreateProject creates a new project.
func (c *Client) CreateProject(ctx context.Context, req CreateProjectRequest) (*Project, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/projects", req)
	if err != nil {
		return nil, err
	}
	var project Project
	if err := c.do(httpReq, &project); err != nil {
		return nil, err
	}
	return &project, nil
}

// CreateProjectInOrg creates a new project under a specific organization.
func (c *Client) CreateProjectInOrg(ctx context.Context, orgID string, req CreateProjectRequest) (*Project, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/orgs/"+orgID+"/projects", req)
	if err != nil {
		return nil, err
	}
	var project Project
	if err := c.do(httpReq, &project); err != nil {
		return nil, err
	}
	return &project, nil
}

// GetProject retrieves a project by ID.
func (c *Client) GetProject(ctx context.Context, id string) (*Project, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+id, nil)
	if err != nil {
		return nil, err
	}
	var project Project
	if err := c.do(httpReq, &project); err != nil {
		return nil, err
	}
	return &project, nil
}

// ListProjects lists all projects the caller can access across every org (IAM-driven).
func (c *Client) ListProjects(ctx context.Context) ([]*Project, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects", nil)
	if err != nil {
		return nil, err
	}
	var projects []*Project
	if err := c.do(httpReq, &projects); err != nil {
		return nil, err
	}
	return projects, nil
}

// ListProjectsInOrg lists the projects the caller can access within a single org
// (org id or name). Scoped, unlike ListProjects.
func (c *Client) ListProjectsInOrg(ctx context.Context, orgID string) ([]*Project, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/orgs/"+orgID+"/projects", nil)
	if err != nil {
		return nil, err
	}
	var projects []*Project
	if err := c.do(httpReq, &projects); err != nil {
		return nil, err
	}
	return projects, nil
}

// UpdateProjectEgress sets a project's egress mode (restricted, https, all).
func (c *Client) UpdateProjectEgress(ctx context.Context, id, egress string) (*Project, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPatch, "/api/v1/projects/"+id, UpdateProjectRequest{Egress: egress})
	if err != nil {
		return nil, err
	}
	var project Project
	if err := c.do(httpReq, &project); err != nil {
		return nil, err
	}
	return &project, nil
}

// UpdateOrgQuota sets an organization's operator-only resource ceiling; only the
// non-nil axes of req are changed.
//
// The ceiling is the org's rather than a project's because a tenant decides how
// many projects it has: bounding each one bounded a number the tenant could
// raise by creating another.
func (c *Client) UpdateOrgQuota(ctx context.Context, id string, req SetQuotaRequest) (*Organization, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/admin/orgs/"+id+"/quota", req)
	if err != nil {
		return nil, err
	}
	var org Organization
	if err := c.do(httpReq, &org); err != nil {
		return nil, err
	}
	return &org, nil
}

// ListAudit returns audit log entries, optionally filtered by query params
// (resource_type, resource_id, actor, limit, offset).
func (c *Client) ListAudit(ctx context.Context, query string) ([]*AuditEntry, error) {
	path := "/api/v1/audit"
	if query != "" {
		path += "?" + query
	}
	httpReq, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var entries []*AuditEntry
	if err := c.do(httpReq, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// ListProjectUsage returns a project's metered usage, optionally filtered by
// query params (from, to, group_by, app_id). Quantities only — no cost.
func (c *Client) ListProjectUsage(ctx context.Context, projectID, query string) ([]*UsageEntry, error) {
	path := "/api/v1/projects/" + projectID + "/usage"
	if query != "" {
		path += "?" + query
	}
	httpReq, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var entries []*UsageEntry
	if err := c.do(httpReq, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// ListOrgUsage returns an org-wide usage rollup across its projects, optionally
// filtered by query params (from, to, group_by, app_id).
func (c *Client) ListOrgUsage(ctx context.Context, orgID, query string) ([]*UsageEntry, error) {
	path := "/api/v1/orgs/" + orgID + "/usage"
	if query != "" {
		path += "?" + query
	}
	httpReq, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var entries []*UsageEntry
	if err := c.do(httpReq, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// GetBillingEstimate returns the cost of the period in progress (#111).
//
// Computed, never persisted: the usage is still accruing and the number changes
// every hour. query takes from/to (RFC3339); empty means the current UTC
// calendar month — the same period the close task invoices, so an estimate and
// the bill that replaces it cover the same hours.
//
// Gated on the BILLING axis (#114), not the resource roles: a caller who can
// read the org's usage may still be refused here.
func (c *Client) GetBillingEstimate(ctx context.Context, orgID, query string) (*RatedPeriod, error) {
	path := "/api/v1/orgs/" + orgID + "/billing/estimate"
	if query != "" {
		path += "?" + query
	}
	httpReq, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var rated RatedPeriod
	if err := c.do(httpReq, &rated); err != nil {
		return nil, err
	}
	return &rated, nil
}

// ListPrices returns the platform's current price list.
//
// Unauthenticated — a rate is a published fact, not tenant data. Callable
// before you have an account, which is the point: the only other way to see
// what a resource costs is to have already been billed for it.
func (c *Client) ListPrices(ctx context.Context) ([]*Price, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/prices", nil)
	if err != nil {
		return nil, err
	}
	var prices []*Price
	if err := c.do(httpReq, &prices); err != nil {
		return nil, err
	}
	return prices, nil
}

// OrgPrices returns the rates one org's own invoices will use, and the book
// they come from.
//
// Different from ListPrices, which always answers the published list. An org
// granted another book is billed at rates the public list does not show, and
// quoting the public one at it is how a console and an invoice come to disagree.
func (c *Client) OrgPrices(ctx context.Context, orgID string) (*OrgPriceList, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/orgs/"+orgID+"/billing/prices", nil)
	if err != nil {
		return nil, err
	}
	var out OrgPriceList
	if err := c.do(httpReq, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// OrgPriceBook returns which book an org is billed against. Operator-only, and
// the read half of UpdateOrgPriceBook — a field that can be written and not read
// is one a declarative client cannot refresh.
func (c *Client) OrgPriceBook(ctx context.Context, orgID string) (*BillingAccount, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/admin/orgs/"+orgID+"/price-book", nil)
	if err != nil {
		return nil, err
	}
	var account BillingAccount
	if err := c.do(httpReq, &account); err != nil {
		return nil, err
	}
	return &account, nil
}

// UpdateOrgPriceBook moves an org's billing account onto a price book.
// Operator-only: which rates an org is billed at is a commercial grant, not
// something a tenant sets for itself.
func (c *Client) UpdateOrgPriceBook(ctx context.Context, orgID, book string) (*BillingAccount, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/admin/orgs/"+orgID+"/price-book", SetPriceBookRequest{PriceBook: book})
	if err != nil {
		return nil, err
	}
	var account BillingAccount
	if err := c.do(httpReq, &account); err != nil {
		return nil, err
	}
	return &account, nil
}

// ListInvoices returns an org's invoices, newest period first.
func (c *Client) ListInvoices(ctx context.Context, orgID string) ([]*Invoice, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/orgs/"+orgID+"/billing/invoices", nil)
	if err != nil {
		return nil, err
	}
	var invoices []*Invoice
	if err := c.do(httpReq, &invoices); err != nil {
		return nil, err
	}
	return invoices, nil
}

// GetInvoice returns one invoice with its line items.
func (c *Client) GetInvoice(ctx context.Context, orgID, invoiceID string) (*Invoice, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet,
		"/api/v1/orgs/"+orgID+"/billing/invoices/"+invoiceID, nil)
	if err != nil {
		return nil, err
	}
	var inv Invoice
	if err := c.do(httpReq, &inv); err != nil {
		return nil, err
	}
	return &inv, nil
}

// ListBillingBindings returns an org's billing role grants.
func (c *Client) ListBillingBindings(ctx context.Context, orgID string) ([]*BillingBinding, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/orgs/"+orgID+"/billing/bindings", nil)
	if err != nil {
		return nil, err
	}
	var bindings []*BillingBinding
	if err := c.do(httpReq, &bindings); err != nil {
		return nil, err
	}
	return bindings, nil
}

// GrantBillingBinding grants a billing role to a member. Requires billing.admin.
func (c *Client) GrantBillingBinding(ctx context.Context, orgID, member, memberType, role string) (*BillingBinding, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/orgs/"+orgID+"/billing/bindings",
		GrantBillingBindingRequest{Member: member, MemberType: memberType, Role: role})
	if err != nil {
		return nil, err
	}
	var b BillingBinding
	if err := c.do(httpReq, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// RevokeBillingBinding removes a member's billing role. The server refuses to
// remove an org's last billing admin.
func (c *Client) RevokeBillingBinding(ctx context.Context, orgID, member, memberType string) error {
	path := "/api/v1/orgs/" + orgID + "/billing/bindings/" + url.PathEscape(member)
	if memberType != "" {
		path += "?member_type=" + url.QueryEscape(memberType)
	}
	httpReq, err := c.newRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// GetBudget returns an org's budget and the threshold crossings recorded against
// it. Requires a billing role; a nil Budget means none is set.
func (c *Client) GetBudget(ctx context.Context, orgID string) (*BudgetView, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/orgs/"+orgID+"/billing/budget", nil)
	if err != nil {
		return nil, err
	}
	var view BudgetView
	if err := c.do(httpReq, &view); err != nil {
		return nil, err
	}
	return &view, nil
}

// SetBudget creates or replaces an org's budget. Requires billing.admin.
func (c *Client) SetBudget(ctx context.Context, orgID string, req SetBudgetRequest) (*BillingBudget, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/orgs/"+orgID+"/billing/budget", req)
	if err != nil {
		return nil, err
	}
	var b BillingBudget
	if err := c.do(httpReq, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// DeleteBudget removes an org's budget, stopping further alerts. Crossings
// already recorded are kept. Requires billing.admin.
func (c *Client) DeleteBudget(ctx context.Context, orgID string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/orgs/"+orgID+"/billing/budget", nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// DeleteProject accepts a project's deletion and returns it in `deleting`.
//
// The teardown continues server-side after this returns: emptying a project's
// backup storage is bounded by how much of it there is, not by any timeout this
// client could set. The project is already gone from every list; use
// WaitProjectDeleted to observe the teardown finishing.
func (c *Client) DeleteProject(ctx context.Context, id string) (*Project, error) {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/projects/"+id, nil)
	if err != nil {
		return nil, err
	}
	var project Project
	if err := c.do(httpReq, &project); err != nil {
		return nil, err
	}
	return &project, nil
}

// WaitProjectDeleted blocks until the project's teardown has finished — until
// reading it stops answering — polling every interval.
//
// A caller that needs "deleted" to mean deleted (Terraform, a script that
// recreates the same name) waits here rather than treating the accepted delete
// as complete. It returns ctx.Err() if the caller's deadline passes first; the
// teardown is unaffected and continues.
//
// Both 404 and 403 mean gone: a project's IAM bindings are the project's, so the
// last step of the teardown takes away the permission to read it at the same
// moment as the row. After an accepted delete there is nothing else a refusal
// can mean.
func (c *Client) WaitProjectDeleted(ctx context.Context, id string, interval time.Duration) error {
	for {
		_, err := c.GetProject(ctx, id)
		if err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) &&
				(apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusForbidden) {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// MoveProjectResult is the response from re-homing a project to its org-prefixed
// namespace: the updated project plus any per-app redeploy warnings.
type MoveProjectResult struct {
	Project  *Project `json:"project"`
	Warnings []string `json:"warnings,omitempty"`
}

// MoveProject re-homes a project into its canonical org-prefixed namespace
// (<org short id>-<name>). force proceeds past the stateful-resource guard;
// database/PVC data is not migrated and must be handled separately.
func (c *Client) MoveProject(ctx context.Context, id string, force bool) (*MoveProjectResult, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/projects/"+id+"/move", map[string]bool{"force": force})
	if err != nil {
		return nil, err
	}
	var res MoveProjectResult
	if err := c.do(httpReq, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// UpdateProjectDisplayName changes a project's mutable, cosmetic display name
// (ADR-036). The frozen name — which anchors the k8s namespace and registry path —
// is untouched, so this is a plain label change with no cluster impact.
func (c *Client) UpdateProjectDisplayName(ctx context.Context, id, displayName string) (*Project, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPatch, "/api/v1/projects/"+id, UpdateProjectRequest{DisplayName: displayName})
	if err != nil {
		return nil, err
	}
	var proj Project
	if err := c.do(httpReq, &proj); err != nil {
		return nil, err
	}
	return &proj, nil
}

// CreateApp creates a new app in a project.
func (c *Client) CreateApp(ctx context.Context, projectID string, req CreateAppRequest) (*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/projects/"+projectID+"/apps", req)
	if err != nil {
		return nil, err
	}
	var app App
	if err := c.do(httpReq, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// GetApp retrieves an app by ID.
func (c *Client) GetApp(ctx context.Context, id string) (*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/apps/"+id, nil)
	if err != nil {
		return nil, err
	}
	var app App
	if err := c.do(httpReq, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// ListWorkloadEvents lists the warnings recorded against a project workload —
// admission refusals (quota), image pull failures, container crashes — the
// explanation when a deploy produced no pods and no logs.
func (c *Client) ListWorkloadEvents(ctx context.Context, projectID, workload string) ([]WorkloadEvent, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/workloads/"+workload+"/events", nil)
	if err != nil {
		return nil, err
	}
	var events []WorkloadEvent
	if err := c.do(httpReq, &events); err != nil {
		return nil, err
	}
	return events, nil
}

// ListApps lists all apps in a project.
func (c *Client) ListApps(ctx context.Context, projectID string) ([]*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/apps", nil)
	if err != nil {
		return nil, err
	}
	var apps []*App
	if err := c.do(httpReq, &apps); err != nil {
		return nil, err
	}
	return apps, nil
}

// DeployApp deploys a new revision of an app.
func (c *Client) DeployApp(ctx context.Context, id string, req DeployRequest) (*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/apps/"+id+"/deploy", req)
	if err != nil {
		return nil, err
	}
	var app App
	if err := c.do(httpReq, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// ReconcileApp re-applies an app's runtime from control-plane state, repairing
// drifted cluster objects. Unlike DeployApp it changes no image, writes no
// deployment record, and does not run the release command.
func (c *Client) ReconcileApp(ctx context.Context, id string) (*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/apps/"+id+"/reconcile", nil)
	if err != nil {
		return nil, err
	}
	var app App
	if err := c.do(httpReq, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// ScaleApp updates the scaling configuration for an app.
func (c *Client) ScaleApp(ctx context.Context, id string, req ScaleRequest) (*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/apps/"+id+"/scale", req)
	if err != nil {
		return nil, err
	}
	var app App
	if err := c.do(httpReq, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// UpdateAppDisplayName changes an app's mutable, cosmetic display name (ADR-036).
// The frozen name — which names the k8s resources and the URL — is untouched, so
// this is a plain label change with no downtime or redeploy.
func (c *Client) UpdateAppDisplayName(ctx context.Context, id, displayName string) (*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPatch, "/api/v1/apps/"+id, UpdateAppRequest{DisplayName: displayName})
	if err != nil {
		return nil, err
	}
	var app App
	if err := c.do(httpReq, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// SetAppSecurityContext replaces an app's security context, or clears it when sc
// is nil so the app returns to the platform default. Clearing is how a non-root
// opt-out is revoked once the image no longer needs it.
func (c *Client) SetAppSecurityContext(ctx context.Context, id string, sc *SecurityContext) (*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/apps/"+id+"/security-context",
		SetSecurityContextRequest{SecurityContext: sc})
	if err != nil {
		return nil, err
	}
	var app App
	if err := c.do(httpReq, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// UpdateAppURLSlug sets or clears an app's optional vanity host override (ADR-040).
// An empty slug clears it, reverting the host to the derived label; a non-empty slug
// makes the app reachable at <slug>.app.<platform_domain>. Always-on mode only.
func (c *Client) UpdateAppURLSlug(ctx context.Context, id, slug string) (*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPatch, "/api/v1/apps/"+id, UpdateAppRequest{URLSlug: &slug})
	if err != nil {
		return nil, err
	}
	var app App
	if err := c.do(httpReq, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// SetAppDatabase binds the app's unprefixed DATABASE_URL to one of its project's
// databases by name or id (#544). An empty ref clears the binding: DATABASE_URL
// then falls back to the project's sole database, or is omitted entirely when the
// project has several.
func (c *Client) SetAppDatabase(ctx context.Context, id, databaseRef string) (*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPatch, "/api/v1/apps/"+id, UpdateAppRequest{Database: &databaseRef})
	if err != nil {
		return nil, err
	}
	var app App
	if err := c.do(httpReq, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// SwitchMode migrates an app between hosting modes ("always-on"/"serverless").
func (c *Client) SwitchMode(ctx context.Context, id, mode string) (*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/apps/"+id+"/mode", SwitchModeRequest{Mode: mode})
	if err != nil {
		return nil, err
	}
	var app App
	if err := c.do(httpReq, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// UpdateAppStorage grows an app's persistent volume (grow-only, always-on mode).
func (c *Client) UpdateAppStorage(ctx context.Context, id, storage string) (*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/apps/"+id+"/storage", UpdateStorageRequest{Storage: storage})
	if err != nil {
		return nil, err
	}
	var app App
	if err := c.do(httpReq, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// UpdateAppCommand changes an app's container entrypoint override (command),
// arguments (args), and/or release command. Each is optional: a nil pointer
// leaves the value untouched, a non-nil pointer (including an empty slice)
// replaces it — an empty slice clears the override back to the image defaults
// (or drops the release phase).
func (c *Client) UpdateAppCommand(ctx context.Context, id string, command, args, releaseCommand *[]string) (*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/apps/"+id+"/command", UpdateCommandRequest{Command: command, Args: args, ReleaseCommand: releaseCommand})
	if err != nil {
		return nil, err
	}
	var app App
	if err := c.do(httpReq, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// UpdateAppRoutes replaces an app's per-route visibility carve-outs (#501). The
// list is replace-in-full: an empty one clears every carve-out and puts all
// paths back under the app-wide ingress.
func (c *Client) UpdateAppRoutes(ctx context.Context, id string, routes []Route) (*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/apps/"+id+"/routes", UpdateRoutesRequest{Routes: routes})
	if err != nil {
		return nil, err
	}
	var app App
	if err := c.do(httpReq, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// UpdateAppProbes replaces an app's per-probe liveness/readiness/startup
// overrides (#453). nil clears them, reverting every probe to the shared
// HealthCheck* shorthand.
func (c *Client) UpdateAppProbes(ctx context.Context, id string, probes *ProbeOverrides) (*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/apps/"+id+"/probes", UpdateProbesRequest{Probes: probes})
	if err != nil {
		return nil, err
	}
	var app App
	if err := c.do(httpReq, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// RollbackApp returns an app to a previous release. A rollback that would cross
// a release command fails with a 409 APIError (code
// MIGRATION_CONFIRMATION_REQUIRED) until req.ConfirmMigrations is set.
func (c *Client) RollbackApp(ctx context.Context, id string, req RollbackRequest) (*App, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/apps/"+id+"/rollback", req)
	if err != nil {
		return nil, err
	}
	var app App
	if err := c.do(httpReq, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// GetAppVersion reports what an app is currently running.
func (c *Client) GetAppVersion(ctx context.Context, id string) (*AppVersion, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/apps/"+id+"/version", nil)
	if err != nil {
		return nil, err
	}
	var v AppVersion
	if err := c.do(httpReq, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// DeleteApp deletes an app by ID.
func (c *Client) DeleteApp(ctx context.Context, id string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/apps/"+id, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// GetAppLogs retrieves logs for an app. If req.Follow is true, the returned
// ReadCloser streams logs until closed.
func (c *Client) GetAppLogs(ctx context.Context, id string, req LogsRequest) (io.ReadCloser, error) {
	q := url.Values{}
	if req.Follow {
		q.Set("follow", "true")
	}
	if req.Tail > 0 {
		q.Set("tail", strconv.Itoa(req.Tail))
	}
	if req.Since != "" {
		q.Set("since", req.Since)
	}
	if req.Until != "" {
		q.Set("until", req.Until)
	}
	if req.Timestamps {
		q.Set("timestamps", "true")
	}
	path := "/api/v1/apps/" + id + "/logs"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	httpReq, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		apiErr := &APIError{StatusCode: resp.StatusCode}
		if err := json.NewDecoder(resp.Body).Decode(apiErr); err != nil {
			return nil, &APIError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("HTTP %d", resp.StatusCode)}
		}
		return nil, apiErr
	}

	return resp.Body, nil
}

// GetTraffic retrieves the current traffic split for an app.
func (c *Client) GetTraffic(ctx context.Context, appID string) ([]TrafficTarget, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/apps/"+appID+"/traffic", nil)
	if err != nil {
		return nil, err
	}
	var resp TrafficResponse
	if err := c.do(httpReq, &resp); err != nil {
		return nil, err
	}
	return resp.Targets, nil
}

// SetTraffic sets the traffic split for an app.
func (c *Client) SetTraffic(ctx context.Context, appID string, targets []TrafficTarget) ([]TrafficTarget, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/apps/"+appID+"/traffic", SetTrafficRequest{Targets: targets})
	if err != nil {
		return nil, err
	}
	var resp TrafficResponse
	if err := c.do(httpReq, &resp); err != nil {
		return nil, err
	}
	return resp.Targets, nil
}

// ListRevisions lists all revisions for an app.
func (c *Client) ListRevisions(ctx context.Context, appID string) ([]Revision, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/apps/"+appID+"/revisions", nil)
	if err != nil {
		return nil, err
	}
	var revisions []Revision
	if err := c.do(httpReq, &revisions); err != nil {
		return nil, err
	}
	return revisions, nil
}

// CreateDatabase creates a new database in a project.
func (c *Client) CreateDatabase(ctx context.Context, projectID string, req CreateDatabaseRequest) (*Database, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/projects/"+projectID+"/databases", req)
	if err != nil {
		return nil, err
	}
	var db Database
	if err := c.do(httpReq, &db); err != nil {
		return nil, err
	}
	return &db, nil
}

// GetDatabase retrieves a database by ID.
func (c *Client) GetDatabase(ctx context.Context, id string) (*Database, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/databases/"+id, nil)
	if err != nil {
		return nil, err
	}
	var db Database
	if err := c.do(httpReq, &db); err != nil {
		return nil, err
	}
	return &db, nil
}

// GetDatabaseConnection retrieves a database's live connection info (incl. the
// real CNPG password) for the `db connect` tunnel path.
func (c *Client) GetDatabaseConnection(ctx context.Context, id string) (*DatabaseConnection, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/databases/"+id+"/connection", nil)
	if err != nil {
		return nil, err
	}
	var conn DatabaseConnection
	if err := c.do(httpReq, &conn); err != nil {
		return nil, err
	}
	return &conn, nil
}

// DialTunnel opens the server-side db-connect tunnel (ADR-045): a WebSocket
// carrying raw Postgres wire-protocol bytes, relayed by the API to the
// database's CNPG -rw Service. No k8s/FKE credentials involved — this rides
// the same Authorization header as every other API call. Call once per local
// TCP connection to relay (so e.g. `pg_dump -j N` gets N independent tunnels).
func (c *Client) DialTunnel(ctx context.Context, databaseID string) (*websocket.Conn, error) {
	wsURL := strings.Replace(strings.Replace(c.BaseURL, "https://", "wss://", 1), "http://", "ws://", 1)
	wsURL += "/api/v1/databases/" + databaseID + "/tunnel"

	header := http.Header{}
	if c.APIKey != "" {
		header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.Version != "" {
		header.Set(ClientVersionHeader, c.Version)
	}
	ws, resp, err := websocket.DefaultDialer.DialContext(ctx, wsURL, header)
	if err != nil {
		if resp != nil {
			defer resp.Body.Close()
			apiErr := &APIError{StatusCode: resp.StatusCode}
			if jerr := json.NewDecoder(resp.Body).Decode(apiErr); jerr != nil {
				return nil, &APIError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("HTTP %d", resp.StatusCode)}
			}
			return nil, apiErr
		}
		return nil, fmt.Errorf("dial tunnel: %w", err)
	}
	return ws, nil
}

// UpdateDatabase reconciles a database's spec (cpu/memory/storage/instances/version/pooler).
func (c *Client) UpdateDatabase(ctx context.Context, id string, req UpdateDatabaseRequest) (*Database, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPatch, "/api/v1/databases/"+id, req)
	if err != nil {
		return nil, err
	}
	var db Database
	if err := c.do(httpReq, &db); err != nil {
		return nil, err
	}
	return &db, nil
}

// ListDatabases lists all databases in a project.
func (c *Client) ListDatabases(ctx context.Context, projectID string) ([]*Database, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/databases", nil)
	if err != nil {
		return nil, err
	}
	var dbs []*Database
	if err := c.do(httpReq, &dbs); err != nil {
		return nil, err
	}
	return dbs, nil
}

// DeleteDatabase deletes a database by ID.
func (c *Client) DeleteDatabase(ctx context.Context, id string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/databases/"+id, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// CreateBucket provisions a managed object-storage bucket in a project (ADR-039).
// The response carries the one-time secret access key.
func (c *Client) CreateBucket(ctx context.Context, projectID string, req CreateBucketRequest) (*Bucket, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/projects/"+projectID+"/buckets", req)
	if err != nil {
		return nil, err
	}
	var b Bucket
	if err := c.do(httpReq, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// ListBuckets lists all buckets in a project.
func (c *Client) ListBuckets(ctx context.Context, projectID string) ([]*Bucket, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/buckets", nil)
	if err != nil {
		return nil, err
	}
	var buckets []*Bucket
	if err := c.do(httpReq, &buckets); err != nil {
		return nil, err
	}
	return buckets, nil
}

// GetBucket retrieves a bucket by ID.
func (c *Client) GetBucket(ctx context.Context, id string) (*Bucket, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/buckets/"+id, nil)
	if err != nil {
		return nil, err
	}
	var b Bucket
	if err := c.do(httpReq, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// DeleteBucket deletes a bucket by ID. A non-empty bucket returns a 409 APIError.
func (c *Client) DeleteBucket(ctx context.Context, id string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/buckets/"+id, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// GetBucketCredentials returns a bucket's S3 connection details. The secret is
// only present when a fresh key was minted.
func (c *Client) GetBucketCredentials(ctx context.Context, id string) (*BucketCredentials, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/buckets/"+id+"/credentials", nil)
	if err != nil {
		return nil, err
	}
	var creds BucketCredentials
	if err := c.do(httpReq, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

// MintBucketSessionCredentials mints an expiring S3 credential for this
// caller's own data-plane traffic, carrying whatever the caller may already do
// to the bucket's objects. This — not CreateBucketKey — is how a client reaches
// the S3 plane: a scoped key is a durable credential handed to someone else, and
// creating one is bucket administration (ADR-074).
func (c *Client) MintBucketSessionCredentials(ctx context.Context, id string) (*BucketSessionCredentials, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/buckets/"+id+"/credentials", nil)
	if err != nil {
		return nil, err
	}
	var creds BucketSessionCredentials
	if err := c.do(httpReq, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

// SetBucketQuota updates a bucket's quotas (bytes / object count; 0 = unlimited).
func (c *Client) SetBucketQuota(ctx context.Context, id string, maxSize, maxObjects int64) (*Bucket, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/buckets/"+id+"/quota", SetBucketQuotaRequest{QuotaMaxSize: maxSize, QuotaMaxObjects: maxObjects})
	if err != nil {
		return nil, err
	}
	var b Bucket
	if err := c.do(httpReq, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// ListBucketLifecycleRules lists a bucket's object-expiry rules (#498).
func (c *Client) ListBucketLifecycleRules(ctx context.Context, id string) ([]*BucketLifecycleRule, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/buckets/"+id+"/lifecycle", nil)
	if err != nil {
		return nil, err
	}
	var rules []*BucketLifecycleRule
	if err := c.do(httpReq, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// SetBucketLifecycleRule upserts the expiry rule for one prefix, leaving every
// other prefix's rule alone.
func (c *Client) SetBucketLifecycleRule(ctx context.Context, id string, req SetBucketLifecycleRuleRequest) (*BucketLifecycleRule, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/buckets/"+id+"/lifecycle", req)
	if err != nil {
		return nil, err
	}
	var rule BucketLifecycleRule
	if err := c.do(httpReq, &rule); err != nil {
		return nil, err
	}
	return &rule, nil
}

// DeleteBucketLifecycleRule removes the rule for one prefix (the empty prefix is
// the whole-bucket rule). Dropping every rule is ClearBucketLifecycleRules.
func (c *Client) DeleteBucketLifecycleRule(ctx context.Context, id, prefix string) error {
	q := url.Values{"prefix": {prefix}}
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/buckets/"+id+"/lifecycle?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// ClearBucketLifecycleRules removes every expiry rule on a bucket; nothing on it
// expires afterwards.
func (c *Client) ClearBucketLifecycleRules(ctx context.Context, id string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/buckets/"+id+"/lifecycle?all=true", nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// ListBucketCORSRules lists a bucket's cross-origin rules (#887). The platform's
// own console rule is not among them — it is applied to every bucket and is not
// the tenant's to see or remove.
func (c *Client) ListBucketCORSRules(ctx context.Context, id string) ([]*BucketCORSRule, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/buckets/"+id+"/cors", nil)
	if err != nil {
		return nil, err
	}
	var rules []*BucketCORSRule
	if err := c.do(httpReq, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// SetBucketCORSRules replaces a bucket's whole CORS configuration and returns
// what was written.
func (c *Client) SetBucketCORSRules(ctx context.Context, id string, req SetBucketCORSRequest) ([]*BucketCORSRule, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/buckets/"+id+"/cors", req)
	if err != nil {
		return nil, err
	}
	var rules []*BucketCORSRule
	if err := c.do(httpReq, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// ClearBucketCORSRules removes every rule; no browser origin reaches the bucket
// afterwards.
func (c *Client) ClearBucketCORSRules(ctx context.Context, id string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/buckets/"+id+"/cors", nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// SetBucketWebsite toggles static-website serving on a bucket (#342). Enabling
// serves the bucket anonymously over HTTP at the returned WebsiteURL.
func (c *Client) SetBucketWebsite(ctx context.Context, id string, req SetBucketWebsiteRequest) (*Bucket, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/buckets/"+id+"/website", req)
	if err != nil {
		return nil, err
	}
	var b Bucket
	if err := c.do(httpReq, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// PublishWebsiteVersion atomically flips a website bucket to serve an
// already-uploaded version v<version>/ (#439). The same call with a retained
// prior version is a rollback. Upload the build to the version prefix first.
func (c *Client) PublishWebsiteVersion(ctx context.Context, id string, version int) (*Bucket, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/buckets/"+id+"/website/publish", PublishBucketWebsiteRequest{Version: version})
	if err != nil {
		return nil, err
	}
	var b Bucket
	if err := c.do(httpReq, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// ListWebsiteVersions returns the versions of a site that were published,
// newest first (#476) — what it can be rolled back to. This is not the set of
// v<N>/ prefixes in the bucket: a deploy whose publish failed leaves one behind
// that was never served, and promoting it would put a never-live build into
// production.
func (c *Client) ListWebsiteVersions(ctx context.Context, id string) ([]*WebsiteVersion, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/buckets/"+id+"/website/versions", nil)
	if err != nil {
		return nil, err
	}
	var versions []*WebsiteVersion
	if err := c.do(httpReq, &versions); err != nil {
		return nil, err
	}
	return versions, nil
}

// SetBucketURLSlug sets (or clears, with "") a bucket website's vanity host
// label; the site moves to <slug>.web.<platform domain>.
func (c *Client) SetBucketURLSlug(ctx context.Context, id, slug string) (*Bucket, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/buckets/"+id+"/url-slug", SetBucketURLSlugRequest{URLSlug: slug})
	if err != nil {
		return nil, err
	}
	var b Bucket
	if err := c.do(httpReq, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// CreateBucketKey mints a scoped S3 access key for a bucket. The response carries
// the one-time secret access key.
func (c *Client) CreateBucketKey(ctx context.Context, bucketID string, req CreateBucketKeyRequest) (*BucketKey, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/buckets/"+bucketID+"/keys", req)
	if err != nil {
		return nil, err
	}
	var k BucketKey
	if err := c.do(httpReq, &k); err != nil {
		return nil, err
	}
	return &k, nil
}

// ListBucketKeys lists a bucket's scoped keys (never the secret).
func (c *Client) ListBucketKeys(ctx context.Context, bucketID string) ([]*BucketKey, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/buckets/"+bucketID+"/keys", nil)
	if err != nil {
		return nil, err
	}
	var keys []*BucketKey
	if err := c.do(httpReq, &keys); err != nil {
		return nil, err
	}
	return keys, nil
}

// DeleteBucketKey revokes a scoped access key.
func (c *Client) DeleteBucketKey(ctx context.Context, bucketID, accessKeyID string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/buckets/"+bucketID+"/keys/"+accessKeyID, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// UpdateBucketKeyPermissions changes a scoped key's read/write/owner grants.
func (c *Client) UpdateBucketKeyPermissions(ctx context.Context, bucketID, accessKeyID string, req UpdateBucketKeyPermissionsRequest) (*BucketKey, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPatch, "/api/v1/buckets/"+bucketID+"/keys/"+accessKeyID, req)
	if err != nil {
		return nil, err
	}
	var k BucketKey
	if err := c.do(httpReq, &k); err != nil {
		return nil, err
	}
	return &k, nil
}

// ListBucketObjects lists a bucket's objects under prefix, grouping folders at
// "/" (in-browser object browser, #268). An empty prefix lists the root.
func (c *Client) ListBucketObjects(ctx context.Context, bucketID, prefix string) (*ObjectListing, error) {
	path := "/api/v1/buckets/" + bucketID + "/objects?delimiter=" + url.QueryEscape("/")
	if prefix != "" {
		path += "&prefix=" + url.QueryEscape(prefix)
	}
	httpReq, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var listing ObjectListing
	if err := c.do(httpReq, &listing); err != nil {
		return nil, err
	}
	return &listing, nil
}

// PresignBucketObject mints a presigned S3 URL for a GET (download) or PUT
// (upload) so the browser transfers bytes straight to the object store (#268).
func (c *Client) PresignBucketObject(ctx context.Context, bucketID string, req PresignObjectRequest) (*PresignResponse, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/buckets/"+bucketID+"/objects/presign", req)
	if err != nil {
		return nil, err
	}
	var res PresignResponse
	if err := c.do(httpReq, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// DeleteBucketObject deletes a single object from a bucket (#268).
func (c *Client) DeleteBucketObject(ctx context.Context, bucketID, key string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/buckets/"+bucketID+"/objects?key="+url.QueryEscape(key), nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// BindAppBucket binds a bucket to an app, injecting its S3_*/AWS_* credentials
// into the app's pod (#264). readOnly requests a read-only scoped key.
func (c *Client) BindAppBucket(ctx context.Context, appID, bucketID string, readOnly bool) (*AppBucketBinding, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/apps/"+appID+"/buckets", BindBucketRequest{BucketID: bucketID, ReadOnly: readOnly})
	if err != nil {
		return nil, err
	}
	var b AppBucketBinding
	if err := c.do(httpReq, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// ListAppBuckets lists an app's bucket bindings (never the secret).
func (c *Client) ListAppBuckets(ctx context.Context, appID string) ([]*AppBucketBinding, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/apps/"+appID+"/buckets", nil)
	if err != nil {
		return nil, err
	}
	var bindings []*AppBucketBinding
	if err := c.do(httpReq, &bindings); err != nil {
		return nil, err
	}
	return bindings, nil
}

// UnbindAppBucket removes an app ⇄ bucket binding, dropping the injected creds.
func (c *Client) UnbindAppBucket(ctx context.Context, appID, bucketID string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/apps/"+appID+"/buckets/"+bucketID, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// ListBackups lists all backups for a database.
func (c *Client) ListBackups(ctx context.Context, dbID string) ([]DatabaseBackup, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/databases/"+dbID+"/backups", nil)
	if err != nil {
		return nil, err
	}
	var backups []DatabaseBackup
	if err := c.do(httpReq, &backups); err != nil {
		return nil, err
	}
	return backups, nil
}

// CreateBackup triggers a manual backup for a database.
func (c *Client) CreateBackup(ctx context.Context, dbID string) (*DatabaseBackup, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/databases/"+dbID+"/backups", nil)
	if err != nil {
		return nil, err
	}
	var backup DatabaseBackup
	if err := c.do(httpReq, &backup); err != nil {
		return nil, err
	}
	return &backup, nil
}

// DeleteBackup removes a single managed backup (its Backup CR + object-store
// artifact). The API refuses to delete the backup anchoring the recovery window.
func (c *Client) DeleteBackup(ctx context.Context, dbID, name string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/databases/"+dbID+"/backups/"+name, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// GetBackupConfig retrieves the backup configuration for a database.
func (c *Client) GetBackupConfig(ctx context.Context, dbID string) (*BackupConfig, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/databases/"+dbID+"/backup-config", nil)
	if err != nil {
		return nil, err
	}
	var config BackupConfig
	if err := c.do(httpReq, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

// UpdateBackupConfig updates the backup configuration for a database.
func (c *Client) UpdateBackupConfig(ctx context.Context, dbID string, req UpdateBackupConfigRequest) error {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/databases/"+dbID+"/backup-config", req)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// GetBackupDestination retrieves a database's external (BYOB) backup destination.
func (c *Client) GetBackupDestination(ctx context.Context, dbID string) (*BackupDestination, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/databases/"+dbID+"/backup-destination", nil)
	if err != nil {
		return nil, err
	}
	var dest BackupDestination
	if err := c.do(httpReq, &dest); err != nil {
		return nil, err
	}
	return &dest, nil
}

// SetBackupDestination configures (or replaces) a database's external backup
// destination — the customer's own bucket, keyless.
func (c *Client) SetBackupDestination(ctx context.Context, dbID string, req SetBackupDestinationRequest) (*BackupDestination, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/databases/"+dbID+"/backup-destination", req)
	if err != nil {
		return nil, err
	}
	var out BackupDestination
	if err := c.do(httpReq, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteBackupDestination removes a database's external backup destination.
func (c *Client) DeleteBackupDestination(ctx context.Context, dbID string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/databases/"+dbID+"/backup-destination", nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// RunBackupDestination starts an on-demand external backup (pg_dump → the
// customer bucket). The backup runs asynchronously as a k8s Job.
func (c *Client) RunBackupDestination(ctx context.Context, dbID string) (*BackupDestinationRun, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/databases/"+dbID+"/backup-destination/sync", nil)
	if err != nil {
		return nil, err
	}
	var run BackupDestinationRun
	if err := c.do(httpReq, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// RestoreBackupDestination restores a database from a dump in the customer bucket
// (pg_restore). object names the dump; empty restores the latest.
func (c *Client) RestoreBackupDestination(ctx context.Context, dbID, object string) (*BackupDestinationRun, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/databases/"+dbID+"/backup-destination/restore", map[string]string{"object": object})
	if err != nil {
		return nil, err
	}
	var run BackupDestinationRun
	if err := c.do(httpReq, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// RestoreDatabase restores a database from a backup.
func (c *Client) RestoreDatabase(ctx context.Context, dbID string, req RestoreRequest) (*Database, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/databases/"+dbID+"/restore", req)
	if err != nil {
		return nil, err
	}
	var db Database
	if err := c.do(httpReq, &db); err != nil {
		return nil, err
	}
	return &db, nil
}

// AddDomain adds a custom domain to an app. mode selects the attachment behavior
// (ADR-044); an empty mode defaults to "verified".
func (c *Client) AddDomain(ctx context.Context, appID, domain, mode string) (*Domain, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/apps/"+appID+"/domains", DomainRequest{Domain: domain, Mode: mode})
	if err != nil {
		return nil, err
	}
	var d Domain
	if err := c.do(httpReq, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// ListDomains lists all custom domains for an app.
func (c *Client) ListDomains(ctx context.Context, appID string) ([]*Domain, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/apps/"+appID+"/domains", nil)
	if err != nil {
		return nil, err
	}
	var domains []*Domain
	if err := c.do(httpReq, &domains); err != nil {
		return nil, err
	}
	return domains, nil
}

// VerifyDomain re-checks TXT ownership + DNS pointing for a custom domain and
// returns the full verification breakdown plus the records still needed.
func (c *Client) VerifyDomain(ctx context.Context, appID string, domain string) (*DomainVerification, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/apps/"+appID+"/domains/"+domain+"/verify", nil)
	if err != nil {
		return nil, err
	}
	var v DomainVerification
	if err := c.do(httpReq, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// SetDomainRoutes replaces a domain's path->app route table (#581, ADR-060),
// fanning one hostname out to several apps by path prefix. Replace-in-full: an
// empty list clears the fan-out.
func (c *Client) SetDomainRoutes(ctx context.Context, appID, domain string, routes []DomainRoute) (*Domain, error) {
	if routes == nil {
		routes = []DomainRoute{}
	}
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/apps/"+appID+"/domains/"+domain+"/routes", SetDomainRoutesRequest{Routes: routes})
	if err != nil {
		return nil, err
	}
	var d Domain
	if err := c.do(httpReq, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// RemoveDomain removes a custom domain from an app.
// AddBucketDomain claims a custom domain for a website bucket (#342).
func (c *Client) AddBucketDomain(ctx context.Context, bucketID, domain string) (*Domain, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/buckets/"+bucketID+"/domains", DomainRequest{Domain: domain})
	if err != nil {
		return nil, err
	}
	var d Domain
	if err := c.do(httpReq, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// ListBucketDomains lists a website bucket's custom domains.
func (c *Client) ListBucketDomains(ctx context.Context, bucketID string) ([]*Domain, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/buckets/"+bucketID+"/domains", nil)
	if err != nil {
		return nil, err
	}
	var domains []*Domain
	if err := c.do(httpReq, &domains); err != nil {
		return nil, err
	}
	return domains, nil
}

// VerifyBucketDomain re-checks a website bucket domain's ownership/pointing/cert.
func (c *Client) VerifyBucketDomain(ctx context.Context, bucketID, domain string) (*DomainVerification, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/buckets/"+bucketID+"/domains/"+domain+"/verify", nil)
	if err != nil {
		return nil, err
	}
	var vr DomainVerification
	if err := c.do(httpReq, &vr); err != nil {
		return nil, err
	}
	return &vr, nil
}

// RemoveBucketDomain removes a custom domain from a website bucket.
func (c *Client) RemoveBucketDomain(ctx context.Context, bucketID, domain string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/buckets/"+bucketID+"/domains/"+domain, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

func (c *Client) RemoveDomain(ctx context.Context, appID string, domain string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/apps/"+appID+"/domains/"+domain, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// SetConfig sets a config value for an app.
func (c *Client) SetConfig(ctx context.Context, appID, key, value string, isSecret bool) (*AppConfig, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/apps/"+appID+"/config", SetConfigRequest{
		Key:      key,
		Value:    value,
		IsSecret: isSecret,
	})
	if err != nil {
		return nil, err
	}
	var cfg AppConfig
	if err := c.do(httpReq, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ListConfig lists all config values for an app.
func (c *Client) ListConfig(ctx context.Context, appID string) ([]*AppConfig, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/apps/"+appID+"/config", nil)
	if err != nil {
		return nil, err
	}
	var configs []*AppConfig
	if err := c.do(httpReq, &configs); err != nil {
		return nil, err
	}
	return configs, nil
}

// UnsetConfig removes a config value from an app.
func (c *Client) UnsetConfig(ctx context.Context, appID, key string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/apps/"+appID+"/config/"+key, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// SetupWebhook configures a GitHub webhook for auto-deploy on an app.
func (c *Client) SetupWebhook(ctx context.Context, appID string, req SetupWebhookRequest) (*AppWebhook, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/apps/"+appID+"/webhook", req)
	if err != nil {
		return nil, err
	}
	var wh AppWebhook
	if err := c.do(httpReq, &wh); err != nil {
		return nil, err
	}
	return &wh, nil
}

// GetWebhook retrieves the webhook configuration for an app.
func (c *Client) GetWebhook(ctx context.Context, appID string) (*AppWebhook, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/apps/"+appID+"/webhook", nil)
	if err != nil {
		return nil, err
	}
	var wh AppWebhook
	if err := c.do(httpReq, &wh); err != nil {
		return nil, err
	}
	return &wh, nil
}

// RemoveWebhook removes the webhook configuration for an app.
func (c *Client) RemoveWebhook(ctx context.Context, appID string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/apps/"+appID+"/webhook", nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// --- Deployment methods ---

// ListDeployments lists deployment history for an app.
func (c *Client) ListDeployments(ctx context.Context, appID string) ([]*Deployment, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/apps/"+appID+"/deployments", nil)
	if err != nil {
		return nil, err
	}
	var deployments []*Deployment
	if err := c.do(httpReq, &deployments); err != nil {
		return nil, err
	}
	return deployments, nil
}

// GetDeployment retrieves a single deployment by ID.
func (c *Client) GetDeployment(ctx context.Context, appID, deploymentID string) (*Deployment, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/apps/"+appID+"/deployments/"+deploymentID, nil)
	if err != nil {
		return nil, err
	}
	var deployment Deployment
	if err := c.do(httpReq, &deployment); err != nil {
		return nil, err
	}
	return &deployment, nil
}

// --- Organization methods ---

func (c *Client) ListOrgs(ctx context.Context) ([]*Organization, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/orgs", nil)
	if err != nil {
		return nil, err
	}
	var orgs []*Organization
	if err := c.do(httpReq, &orgs); err != nil {
		return nil, err
	}
	return orgs, nil
}

// GetOrg retrieves an organization by any reference it answers to: its uuid, its
// frozen short id or its display name (ADR-094). Resolving a spelling before
// calling this is never necessary — the API exchanges the reference for the org
// at its route boundary (ADR-121).
func (c *Client) GetOrg(ctx context.Context, id string) (*Organization, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/orgs/"+id, nil)
	if err != nil {
		return nil, err
	}
	var org Organization
	if err := c.do(httpReq, &org); err != nil {
		return nil, err
	}
	return &org, nil
}

// UpdateOrgFKE toggles an organization's FKE entitlement (kubectl/kubeconfig
// access). Operator-only: it lives under /admin, which is gated on administrate
// over the platform-operator org (#710).
func (c *Client) UpdateOrgFKE(ctx context.Context, id string, enabled bool) (*Organization, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/admin/orgs/"+id+"/fke", SetOrgFKERequest{Enabled: &enabled})
	if err != nil {
		return nil, err
	}
	var org Organization
	if err := c.do(httpReq, &org); err != nil {
		return nil, err
	}
	return &org, nil
}

// UpdateOrgDisplayName changes an organization's mutable, cosmetic display name.
// The frozen name and short_id are untouched. Gated on org-admin server-side.
func (c *Client) UpdateOrgDisplayName(ctx context.Context, id, displayName string) (*Organization, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPatch, "/api/v1/orgs/"+id, UpdateOrgRequest{DisplayName: displayName})
	if err != nil {
		return nil, err
	}
	var org Organization
	if err := c.do(httpReq, &org); err != nil {
		return nil, err
	}
	return &org, nil
}

// ListOrgMembers lists all members of an organization.
func (c *Client) ListOrgMembers(ctx context.Context, orgID string) ([]*OrgMember, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/orgs/"+orgID+"/members", nil)
	if err != nil {
		return nil, err
	}
	var members []*OrgMember
	if err := c.do(httpReq, &members); err != nil {
		return nil, err
	}
	return members, nil
}

// ProvisionUser creates a new user in an existing organization and mints an
// API key. Admin-only; replaces self-service registration in the internal model.
func (c *Client) ProvisionUser(ctx context.Context, orgID, email, name, role string) (*RegisterResponse, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/orgs/"+orgID+"/users", ProvisionUserRequest{
		Email: email,
		Name:  name,
		Role:  role,
	})
	if err != nil {
		return nil, err
	}
	var resp RegisterResponse
	if err := c.do(httpReq, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// InviteOrgMember invites a user to an organization by email.
func (c *Client) InviteOrgMember(ctx context.Context, orgID, email, role string) (*OrgMember, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/orgs/"+orgID+"/members", InviteOrgMemberRequest{
		Email: email,
		Role:  role,
	})
	if err != nil {
		return nil, err
	}
	var member OrgMember
	if err := c.do(httpReq, &member); err != nil {
		return nil, err
	}
	return &member, nil
}

// UpdateOrgMemberRole updates a member's role in an organization.
func (c *Client) UpdateOrgMemberRole(ctx context.Context, orgID, userID, role string) error {
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/orgs/"+orgID+"/members/"+userID, UpdateOrgMemberRoleRequest{
		Role: role,
	})
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// RemoveOrgMember removes a member from an organization.
func (c *Client) RemoveOrgMember(ctx context.Context, orgID, userID string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/orgs/"+orgID+"/members/"+userID, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// --- Service Account methods ---

// CreateServiceAccount creates a new service account in a project.
// CreateTrustBinding creates a per-project OIDC federation trust binding: a repo
// (matched by subject_pattern) on an issuer may assume the given service account.
func (c *Client) CreateTrustBinding(ctx context.Context, projectID string, req CreateTrustBindingRequest) (*TrustBinding, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/projects/"+projectID+"/federation", req)
	if err != nil {
		return nil, err
	}
	var binding TrustBinding
	if err := c.do(httpReq, &binding); err != nil {
		return nil, err
	}
	return &binding, nil
}

// ListTrustBindings lists the OIDC federation trust bindings in a project.
func (c *Client) ListTrustBindings(ctx context.Context, projectID string) ([]*TrustBinding, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/federation", nil)
	if err != nil {
		return nil, err
	}
	var bindings []*TrustBinding
	if err := c.do(httpReq, &bindings); err != nil {
		return nil, err
	}
	return bindings, nil
}

// DeleteTrustBinding deletes an OIDC federation trust binding in a project.
func (c *Client) DeleteTrustBinding(ctx context.Context, projectID, bindingID string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/projects/"+projectID+"/federation/"+bindingID, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

func (c *Client) CreateServiceAccount(ctx context.Context, projectID string, req CreateServiceAccountRequest) (*ServiceAccount, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/projects/"+projectID+"/service-accounts", req)
	if err != nil {
		return nil, err
	}
	var sa ServiceAccount
	if err := c.do(httpReq, &sa); err != nil {
		return nil, err
	}
	return &sa, nil
}

// UpdateServiceAccountDisplayName changes a service account's mutable, cosmetic
// display name. The frozen name and email are untouched.
func (c *Client) UpdateServiceAccountDisplayName(ctx context.Context, id, displayName string) (*ServiceAccount, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPatch, "/api/v1/service-accounts/"+id, UpdateServiceAccountRequest{DisplayName: displayName})
	if err != nil {
		return nil, err
	}
	var sa ServiceAccount
	if err := c.do(httpReq, &sa); err != nil {
		return nil, err
	}
	return &sa, nil
}

// ListServiceAccounts lists all service accounts in a project.
func (c *Client) ListServiceAccounts(ctx context.Context, projectID string) ([]*ServiceAccount, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/service-accounts", nil)
	if err != nil {
		return nil, err
	}
	var accounts []*ServiceAccount
	if err := c.do(httpReq, &accounts); err != nil {
		return nil, err
	}
	return accounts, nil
}

// DeleteServiceAccount deletes a service account by ID.
func (c *Client) DeleteServiceAccount(ctx context.Context, id string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/service-accounts/"+id, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// CreateServiceAccountKey creates a new key for a service account.
func (c *Client) CreateServiceAccountKey(ctx context.Context, saID string) (*ServiceAccountKey, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/service-accounts/"+saID+"/keys", nil)
	if err != nil {
		return nil, err
	}
	var key ServiceAccountKey
	if err := c.do(httpReq, &key); err != nil {
		return nil, err
	}
	return &key, nil
}

// ListServiceAccountKeys lists all keys for a service account.
func (c *Client) ListServiceAccountKeys(ctx context.Context, saID string) ([]*ServiceAccountKey, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/service-accounts/"+saID+"/keys", nil)
	if err != nil {
		return nil, err
	}
	var keys []*ServiceAccountKey
	if err := c.do(httpReq, &keys); err != nil {
		return nil, err
	}
	return keys, nil
}

// DeleteServiceAccountKey deletes a service account key.
func (c *Client) DeleteServiceAccountKey(ctx context.Context, saID, keyID string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/service-accounts/"+saID+"/keys/"+keyID, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// --- IAM Binding methods ---

// SetIAMBinding creates an IAM binding on a project.
func (c *Client) SetIAMBinding(ctx context.Context, projectID string, req SetIAMBindingRequest) (*IAMBinding, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/projects/"+projectID+"/iam", req)
	if err != nil {
		return nil, err
	}
	var binding IAMBinding
	if err := c.do(httpReq, &binding); err != nil {
		return nil, err
	}
	return &binding, nil
}

// ListIAMBindings lists all IAM bindings for a project.
func (c *Client) ListIAMBindings(ctx context.Context, projectID string) ([]*IAMBinding, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/iam", nil)
	if err != nil {
		return nil, err
	}
	var bindings []*IAMBinding
	if err := c.do(httpReq, &bindings); err != nil {
		return nil, err
	}
	return bindings, nil
}

// RemoveIAMBinding removes an IAM binding from a project.
func (c *Client) RemoveIAMBinding(ctx context.Context, projectID, bindingID string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/projects/"+projectID+"/iam/"+bindingID, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// ListOrgSecrets lists an org's Fogpipe Secrets Manager bundles (key names only).
func (c *Client) ListOrgSecrets(ctx context.Context, orgID string) ([]*OrgSecret, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/orgs/"+orgID+"/secrets", nil)
	if err != nil {
		return nil, err
	}
	var secrets []*OrgSecret
	if err := c.do(httpReq, &secrets); err != nil {
		return nil, err
	}
	return secrets, nil
}

// GetOrgSecret retrieves a single bundle. When reveal is true, the decrypted
// values are returned in Data (requires org write permission).
func (c *Client) GetOrgSecret(ctx context.Context, orgID, name string, reveal bool) (*OrgSecret, error) {
	path := "/api/v1/orgs/" + orgID + "/secrets/" + name
	if reveal {
		path += "?reveal=true"
	}
	httpReq, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var secret OrgSecret
	if err := c.do(httpReq, &secret); err != nil {
		return nil, err
	}
	return &secret, nil
}

// CreateOrgSecret creates a new bundle with the given key/value data, mirrored
// into the given target project ids.
func (c *Client) CreateOrgSecret(ctx context.Context, orgID, name string, data map[string]string, targets []string) (*OrgSecret, error) {
	body := map[string]any{"name": name, "data": data, "targets": targets}
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/orgs/"+orgID+"/secrets", body)
	if err != nil {
		return nil, err
	}
	var secret OrgSecret
	if err := c.do(httpReq, &secret); err != nil {
		return nil, err
	}
	return &secret, nil
}

// UpdateOrgSecret replaces an existing bundle's data and target projects
// (full-desired-state replace).
func (c *Client) UpdateOrgSecret(ctx context.Context, orgID, name string, data map[string]string, targets []string) (*OrgSecret, error) {
	body := map[string]any{"data": data, "targets": targets}
	httpReq, err := c.newRequest(ctx, http.MethodPut, "/api/v1/orgs/"+orgID+"/secrets/"+name, body)
	if err != nil {
		return nil, err
	}
	var secret OrgSecret
	if err := c.do(httpReq, &secret); err != nil {
		return nil, err
	}
	return &secret, nil
}

// DeleteOrgSecret removes a bundle.
func (c *Client) DeleteOrgSecret(ctx context.Context, orgID, name string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/orgs/"+orgID+"/secrets/"+name, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// --- Scheduled jobs (#166) ---

// CreateJob registers a scheduled job in a project.
func (c *Client) CreateJob(ctx context.Context, projectID string, req CreateJobRequest) (*Job, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/projects/"+projectID+"/jobs", req)
	if err != nil {
		return nil, err
	}
	var job Job
	if err := c.do(httpReq, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// ListJobs lists a project's scheduled jobs, each with its most recent run.
func (c *Client) ListJobs(ctx context.Context, projectID string) ([]*Job, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/jobs", nil)
	if err != nil {
		return nil, err
	}
	var jobs []*Job
	if err := c.do(httpReq, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

// GetJob retrieves a scheduled job by ID.
func (c *Client) GetJob(ctx context.Context, id string) (*Job, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/jobs/"+id, nil)
	if err != nil {
		return nil, err
	}
	var job Job
	if err := c.do(httpReq, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// UpdateJob patches a scheduled job.
func (c *Client) UpdateJob(ctx context.Context, id string, req UpdateJobRequest) (*Job, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPatch, "/api/v1/jobs/"+id, req)
	if err != nil {
		return nil, err
	}
	var job Job
	if err := c.do(httpReq, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// DeleteJob removes a scheduled job and its run history.
func (c *Client) DeleteJob(ctx context.Context, id string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/jobs/"+id, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// RunJob fires a job now, outside its schedule, and returns the run record.
func (c *Client) RunJob(ctx context.Context, id string) (*JobRun, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/jobs/"+id+"/run", nil)
	if err != nil {
		return nil, err
	}
	var run JobRun
	if err := c.do(httpReq, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// ListJobRuns returns a job's run history, newest first.
func (c *Client) ListJobRuns(ctx context.Context, id string) ([]*JobRun, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/jobs/"+id+"/runs", nil)
	if err != nil {
		return nil, err
	}
	var runs []*JobRun
	if err := c.do(httpReq, &runs); err != nil {
		return nil, err
	}
	return runs, nil
}

// GetJobLogs returns the output of one run, defaulting to the most recent when
// runName is empty.
func (c *Client) GetJobLogs(ctx context.Context, id, runName string) (string, error) {
	path := "/api/v1/jobs/" + id + "/logs"
	if runName != "" {
		path += "?run=" + url.QueryEscape(runName)
	}
	httpReq, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Logs string `json:"logs"`
	}
	if err := c.do(httpReq, &out); err != nil {
		return "", err
	}
	return out.Logs, nil
}

// --- Managed GitHub Actions runners (#418, ADR-064) ---

// CreateRunner declares a runner pool in a project.
func (c *Client) CreateRunner(ctx context.Context, projectID string, req CreateRunnerRequest) (*Runner, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/projects/"+projectID+"/runners", req)
	if err != nil {
		return nil, err
	}
	var runner Runner
	if err := c.do(httpReq, &runner); err != nil {
		return nil, err
	}
	return &runner, nil
}

// ListRunners lists a project's runner pools.
func (c *Client) ListRunners(ctx context.Context, projectID string) ([]*Runner, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/runners", nil)
	if err != nil {
		return nil, err
	}
	var runners []*Runner
	if err := c.do(httpReq, &runners); err != nil {
		return nil, err
	}
	return runners, nil
}

// GetRunner retrieves a runner pool by ID.
func (c *Client) GetRunner(ctx context.Context, id string) (*Runner, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/runners/"+id, nil)
	if err != nil {
		return nil, err
	}
	var runner Runner
	if err := c.do(httpReq, &runner); err != nil {
		return nil, err
	}
	return &runner, nil
}

// UpdateRunner patches a runner pool.
func (c *Client) UpdateRunner(ctx context.Context, id string, req UpdateRunnerRequest) (*Runner, error) {
	httpReq, err := c.newRequest(ctx, http.MethodPatch, "/api/v1/runners/"+id, req)
	if err != nil {
		return nil, err
	}
	var runner Runner
	if err := c.do(httpReq, &runner); err != nil {
		return nil, err
	}
	return &runner, nil
}

// DeleteRunner removes a runner pool and deregisters it from GitHub.
func (c *Client) DeleteRunner(ctx context.Context, id string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/runners/"+id, nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// --- GitHub connection (#790) ---

// StartGitHubConnect returns the URL to open in a browser to install the
// Fogpipe GitHub App and bind the account to a project. Ownership is proved
// there, by GitHub, not here.
// account is optional and only disambiguates when the caller administers
// several accounts with the app installed; it selects within what GitHub
// confirms and can never widen it.
func (c *Client) StartGitHubConnect(ctx context.Context, projectID, account string) (*GitHubConnectStart, error) {
	path := "/api/v1/projects/" + projectID + "/github/connect"
	if account != "" {
		path += "?account=" + url.QueryEscape(account)
	}
	httpReq, err := c.newRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}
	var start GitHubConnectStart
	if err := c.do(httpReq, &start); err != nil {
		return nil, err
	}
	return &start, nil
}

// GetGitHubConnection returns the GitHub account a project is connected to.
func (c *Client) GetGitHubConnection(ctx context.Context, projectID string) (*GitHubConnection, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/github", nil)
	if err != nil {
		return nil, err
	}
	var conn GitHubConnection
	if err := c.do(httpReq, &conn); err != nil {
		return nil, err
	}
	return &conn, nil
}

// DisconnectGitHub drops a project's GitHub connection.
func (c *Client) DisconnectGitHub(ctx context.Context, projectID string) error {
	httpReq, err := c.newRequest(ctx, http.MethodDelete, "/api/v1/projects/"+projectID+"/github", nil)
	if err != nil {
		return err
	}
	return c.do(httpReq, nil)
}

// ProjectStatus fetches the whole-project status document (GET
// /projects/{id}/status) and the ETag identifying the state it describes.
//
// Conditional when ifNoneMatch is a previous response's ETag: an unchanged
// project answers 304 and returns (nil, ifNoneMatch, nil), so a client watching
// a project pays for a document only when there is a new one. A nil status with
// a nil error therefore means "what you already have is still current" and is
// never an empty project.
func (c *Client) ProjectStatus(ctx context.Context, projectID, ifNoneMatch string) (*ProjectStatus, string, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/status", nil)
	if err != nil {
		return nil, "", err
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil, ifNoneMatch, nil
	}
	if resp.StatusCode >= 400 {
		return nil, "", responseError(resp)
	}
	var status ProjectStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, "", fmt.Errorf("decoding response: %w", err)
	}
	return &status, resp.Header.Get("ETag"), nil
}
