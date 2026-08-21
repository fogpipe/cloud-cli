package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Project represents a Fogpipe project.
type Project struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	Name           string    `json:"name"`
	DisplayName    string    `json:"display_name"`
	Status         string    `json:"status"` // active, suspended, deleting
	Namespace      string    `json:"namespace"`
	Egress         string    `json:"egress"`
	MaxCPU         string    `json:"max_cpu"`
	MaxMemory      string    `json:"max_memory"`
	MaxPods        int       `json:"max_pods"`
	MaxStorage     string    `json:"max_storage"`
	IsPlatform     bool      `json:"is_platform,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// CreateProjectRequest is the request body for creating a project.
type CreateProjectRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Egress      string `json:"egress,omitempty"`
}

// AuditEntry is one record from the audit log.
type AuditEntry struct {
	ID           string         `json:"id"`
	Timestamp    time.Time      `json:"ts"`
	ActorType    string         `json:"actor_type"`
	Actor        string         `json:"actor"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Details      map[string]any `json:"details,omitempty"`
}

// UsageEntry is one aggregated slice of metered usage — a quantity of one
// resource type over a period, along whichever axis was requested (#675).
//
// Identity is a name snapshot rather than a join: usage outlives the resource
// that produced it, so a deleted app still reports what it consumed.
type UsageEntry struct {
	ProjectID   string `json:"project_id,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
	// AppID names either an app or a database; ResourceType distinguishes them
	// (compute.*/volume.* = app, database.* = database). Empty means the usage
	// belongs to the project rather than to any one workload.
	AppID   string     `json:"app_id,omitempty"`
	AppName string     `json:"app_name,omitempty"`
	Day     *time.Time `json:"day,omitempty"` // set only when grouped by day
	// ResourceType is an opaque token (compute.cpu, database.storage, …). New
	// ones appear as metering grows — never enumerate them.
	ResourceType string `json:"resource_type"`
	Unit         string `json:"unit"`
	// Quantity is a decimal string, not a number: the underlying column is
	// NUMERIC because a float sum over a month of hourly rows drifts. Parse it
	// only to format it.
	Quantity string `json:"quantity"`
}

// RatedLine is one priced component of a period's usage (#112).
//
// One line per (resource type, RATE) — a period spanning a price change yields
// two lines for the same resource, each at the rate that actually applied.
// Grouping these by resource type alone double-counts or silently picks one
// rate.
//
// Quantity, UnitPrice and Amount are decimal strings for the same reason
// UsageEntry.Quantity is: the arithmetic happens in Postgres NUMERIC and a
// float64 round-trip loses exactness money cannot afford. Parse only to format.
type RatedLine struct {
	ResourceType string `json:"resource_type"`
	Unit         string `json:"unit"`
	Quantity     string `json:"quantity"`
	// Empty when Priced is false.
	UnitPrice string `json:"unit_price,omitempty"`
	Amount    string `json:"amount,omitempty"`
	Currency  string `json:"currency"`
	// Priced is false when the resource is metered but has no price in effect.
	// Reported rather than billed at zero — metering keeps adding resource types
	// and each arrives before anyone has priced it.
	Priced bool `json:"priced"`
}

// RatedPeriod is what a scope's usage came to over a period.
//
// Total covers the priced lines only, and UnpricedTypes names what it left out.
// A non-empty UnpricedTypes means Total is an understatement and a surface
// showing it has to say so.
type RatedPeriod struct {
	Lines         []*RatedLine `json:"lines"`
	Total         string       `json:"total"`
	Currency      string       `json:"currency"`
	UnpricedTypes []string     `json:"unpriced_types,omitempty"`
}

// Price is what one unit of a metered resource costs.
//
// The unit lives on the usage, not here — it is a property of how a resource is
// metered rather than of what it costs. UnitPrice is a decimal string: rates
// carry more precision than a cent (EUR 0.00005 per gib-hour is real) and JSON
// numbers are floats.
type Price struct {
	ResourceType  string    `json:"resource_type"`
	Currency      string    `json:"currency"`
	UnitPrice     string    `json:"unit_price"`
	EffectiveFrom time.Time `json:"effective_from"`
}

// Invoice is what an org owed for one closed period (#111). Amounts are decimal
// strings; a finalized invoice is immutable.
type Invoice struct {
	ID               string    `json:"id"`
	BillingAccountID string    `json:"billing_account_id"`
	OrgID            string    `json:"org_id"`
	PeriodStart      time.Time `json:"period_start"`
	PeriodEnd        time.Time `json:"period_end"`
	Status           string    `json:"status"` // draft, finalized, void
	Currency         string    `json:"currency"`
	// One amount, summed from the lines. No subtotal or tax: VAT is #115 and
	// arrives with the code that computes it.
	Total       string     `json:"total"`
	FinalizedAt *time.Time `json:"finalized_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	// Lines is populated only when fetching a single invoice.
	Lines []*InvoiceLineItem `json:"lines,omitempty"`
}

// InvoiceLineItem is one (resource type, project, rate) component of an invoice.
//
// UnitPrice is the rate this was BILLED at, stored on the line rather than
// looked up — an invoice that referenced the current price would be rewritten by
// the next reprice and a dispute would have no evidence left.
type InvoiceLineItem struct {
	ResourceType string `json:"resource_type"`
	ProjectID    string `json:"project_id,omitempty"`
	ProjectName  string `json:"project_name,omitempty"`
	Unit         string `json:"unit"`
	Quantity     string `json:"quantity"`
	UnitPrice    string `json:"unit_price"`
	Amount       string `json:"amount"`
}

// BillingBudget is what an org means to spend in a period, with the percentages
// of it at which it wants to be told (#109). An alerting threshold, never a cap:
// nothing is refused when it is crossed. Amount is a decimal string like every
// other money value here.
type BillingBudget struct {
	OrgID      string    `json:"org_id"`
	Amount     string    `json:"amount"`
	Currency   string    `json:"currency"`
	Thresholds []int     `json:"thresholds"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// BillingBudgetAlert is one recorded threshold crossing. Amount is the estimate
// at the moment it crossed, which exists nowhere else afterwards — the estimate
// keeps moving.
type BillingBudgetAlert struct {
	OrgID            string    `json:"org_id"`
	PeriodStart      time.Time `json:"period_start"`
	ThresholdPercent int       `json:"threshold_percent"`
	Amount           string    `json:"amount"`
	BudgetAmount     string    `json:"budget_amount"`
	Currency         string    `json:"currency"`
	CreatedAt        time.Time `json:"created_at"`
}

// BudgetView is a budget together with the crossings recorded against it. Budget
// is null when the org has set none, which is an ordinary state.
type BudgetView struct {
	Budget *BillingBudget        `json:"budget"`
	Alerts []*BillingBudgetAlert `json:"alerts"`
}

// SetBudgetRequest sets an org's budget. Empty Thresholds means the defaults
// (50/90/100).
type SetBudgetRequest struct {
	Amount     string `json:"amount"`
	Currency   string `json:"currency,omitempty"`
	Thresholds []int  `json:"thresholds,omitempty"`
}

// BillingBinding grants a billing role on an org (#114). A SEPARATE axis from
// the resource roles — an org owner without one of these cannot see the bill.
type BillingBinding struct {
	ID         string    `json:"id"`
	OrgID      string    `json:"org_id"`
	MemberType string    `json:"member_type"`
	Member     string    `json:"member"`
	Role       string    `json:"role"` // billing.viewer, billing.admin
	CreatedAt  time.Time `json:"created_at"`
}

// Billing roles (#114).
const (
	BillingViewer = "billing.viewer"
	BillingAdmin  = "billing.admin"
)

// GrantBillingBindingRequest grants a billing role to a member.
type GrantBillingBindingRequest struct {
	Member     string `json:"member"`
	MemberType string `json:"member_type,omitempty"`
	Role       string `json:"role"`
}

// UpdateProjectRequest is the request body for updating a project.
type UpdateProjectRequest struct {
	DisplayName string `json:"display_name,omitempty"`
	Egress      string `json:"egress,omitempty"`
}

// SetQuotaRequest carries the ADR-035 resource caps. Operator-only: it targets
// PUT /admin/projects/{id}/quota, not the tenant PATCH (#710).
type SetQuotaRequest struct {
	MaxCPU     *string `json:"max_cpu,omitempty"`
	MaxMemory  *string `json:"max_memory,omitempty"`
	MaxPods    *int    `json:"max_pods,omitempty"`
	MaxStorage *string `json:"max_storage,omitempty"`
}

// TrustBinding is a per-project OIDC federation trust binding: a repo (matched by
// SubjectPattern) on Issuer, carrying Audience, may assume ServiceAccountID.
type TrustBinding struct {
	ID               string    `json:"id"`
	Issuer           string    `json:"issuer"`
	Audience         string    `json:"audience"`
	SubjectPattern   string    `json:"subject_pattern"`
	ServiceAccountID string    `json:"service_account_id"`
	TokenTTLSeconds  int       `json:"token_ttl_seconds"`
	CreatedAt        time.Time `json:"created_at"`
}

// CreateTrustBindingRequest is the request body for creating a trust binding.
type CreateTrustBindingRequest struct {
	Issuer          string `json:"issuer"`
	Audience        string `json:"audience"`
	SubjectPattern  string `json:"subject_pattern"`
	ServiceAccount  string `json:"service_account"`
	TokenTTLSeconds int    `json:"token_ttl_seconds,omitempty"`
}

// App represents a deployed application.
type App struct {
	ID                  string           `json:"id"`
	ProjectID           string           `json:"project_id"`
	Name                string           `json:"name"`
	DisplayName         string           `json:"display_name"`
	URLSlug             string           `json:"url_slug"`              // optional vanity host override (ADR-040); empty = derived host
	DatabaseID          string           `json:"database_id,omitempty"` // database DATABASE_URL points at (#544); empty = the project's sole database, or none when it has several
	Image               string           `json:"image"`
	Release             string           `json:"release,omitempty"`         // user-named release currently live (#471)
	Command             []string         `json:"command,omitempty"`         // container entrypoint override (empty = image ENTRYPOINT)
	Args                []string         `json:"args,omitempty"`            // container arguments (empty = image CMD)
	ReleaseCommand      []string         `json:"release_command,omitempty"` // run once per deploy, before the new version goes live
	Status              string           `json:"status"`
	URL                 string           `json:"url"`
	Domains             []string         `json:"domains"`
	Replicas            int              `json:"replicas"`
	MinScale            int32            `json:"min_scale"`
	MaxScale            int32            `json:"max_scale"`
	CPULimit            string           `json:"cpu_limit"`
	MemoryLimit         string           `json:"memory_limit"`
	Ingress             string           `json:"ingress"`
	Routes              []Route          `json:"routes,omitempty"` // per-path visibility carve-outs (#501)
	Mode                string           `json:"mode"`
	Type                string           `json:"type"` // "web" (HTTP service) or "worker" (no port, Service or hostname)
	Port                int              `json:"port"` // the API decides it — 8080 on a web app, 0 on a worker
	Storage             string           `json:"storage"`
	KubeServiceAccount  string           `json:"kube_service_account,omitempty"`
	StoragePath         string           `json:"storage_path"`
	ServiceAccountID    string           `json:"service_account_id,omitempty"`
	HealthCheckPath     string           `json:"health_check_path"`
	HealthCheckTimeout  int              `json:"health_check_timeout"`
	HealthCheckInterval int              `json:"health_check_interval"`
	HealthCheckRetries  int              `json:"health_check_retries"`
	Probes              *ProbeOverrides  `json:"probes,omitempty"`           // per-probe path/timing overrides (#453); nil = every probe uses the HealthCheck* shorthand
	VolumeMounts        []VolumeMount    `json:"volume_mounts"`              // ConfigMap/Secret/emptyDir mounts (empty = none)
	SecurityContext     *SecurityContext `json:"security_context,omitempty"` // pod/container hardening (nil = image default)
	CreatedAt           time.Time        `json:"created_at"`
	UpdatedAt           time.Time        `json:"updated_at"`
}

// ProbeOverrides lets liveness, readiness, and startup diverge from the shared
// HealthCheck* shorthand (#453) — e.g. a liveness probe on a cheap,
// dependency-free path while readiness also checks a downstream. A nil field
// means "use HealthCheckPath/Interval/Timeout/Retries".
type ProbeOverrides struct {
	Liveness  *ProbeSpec `json:"liveness,omitempty"`
	Readiness *ProbeSpec `json:"readiness,omitempty"`
	Startup   *ProbeSpec `json:"startup,omitempty"`
}

// ProbeSpec is one probe's HTTP path and timing, each field independently
// optional (zero/empty = fall back to the shared HealthCheck* default).
// SuccessThreshold is only meaningful on Readiness — Kubernetes requires 1 for
// Liveness and Startup.
type ProbeSpec struct {
	Path                string `json:"path,omitempty"`
	InitialDelaySeconds int    `json:"initial_delay_seconds,omitempty"`
	PeriodSeconds       int    `json:"period_seconds,omitempty"`
	TimeoutSeconds      int    `json:"timeout_seconds,omitempty"`
	FailureThreshold    int    `json:"failure_threshold,omitempty"`
	SuccessThreshold    int    `json:"success_threshold,omitempty"`
}

// VolumeMount mounts a ConfigMap/Secret as read-only files, or an emptyDir as
// writable scratch, at a container path.
type VolumeMount struct {
	Source    string `json:"source"`             // "configmap", "secret", or "emptydir"
	Name      string `json:"name"`               // ConfigMap/Secret name (ignored for emptydir)
	MountPath string `json:"mount_path"`         // container path to mount at
	SubPath   string `json:"sub_path,omitempty"` // mount a single key instead of the whole dir
}

// Route carves a path prefix out of an app's app-wide ingress visibility (#501).
// A route marked internal is withheld from the external ingress while staying
// reachable on the app's in-cluster address — where a scheduled job's self-call
// or an admin endpoint wants to live. Always-on mode, ingress=all only.
type Route struct {
	Path       string `json:"path"`       // path prefix, e.g. "/internal/"
	Visibility string `json:"visibility"` // "internal" or "public"
}

// SecurityContext hardens an app's pod/container (nil = image default).
type SecurityContext struct {
	RunAsUser              *int64 `json:"run_as_user,omitempty"`
	RunAsGroup             *int64 `json:"run_as_group,omitempty"`
	FSGroup                *int64 `json:"fs_group,omitempty"`
	RunAsNonRoot           bool   `json:"run_as_non_root,omitempty"`
	ReadOnlyRootFilesystem bool   `json:"read_only_root_filesystem,omitempty"`
}

// CreateAppRequest is the request body for creating an app.
type CreateAppRequest struct {
	Name            string           `json:"name"`
	DisplayName     string           `json:"display_name,omitempty"` // mutable cosmetic label; defaults to Name
	URLSlug         string           `json:"url_slug,omitempty"`     // optional vanity host override (ADR-040)
	Image           string           `json:"image"`
	Command         []string         `json:"command,omitempty"`
	Args            []string         `json:"args,omitempty"`
	ReleaseCommand  []string         `json:"release_command,omitempty"` // run once per deploy, before the new version goes live
	VolumeMounts    []VolumeMount    `json:"volume_mounts,omitempty"`
	SecurityContext *SecurityContext `json:"security_context,omitempty"`
	Port            int              `json:"port,omitempty"`
	Replicas        int              `json:"replicas,omitempty"`
	Ingress         string           `json:"ingress,omitempty"`
	Routes          []Route          `json:"routes,omitempty"` // per-path visibility carve-outs (#501)
	Mode            string           `json:"mode,omitempty"`   // "always-on" (default) or "serverless"
	// Type is the process type: "web" (default) serves HTTP behind a Service;
	// "worker" is a long-lived process with no port, Service or hostname. Frozen
	// at create — no update path changes it.
	Type        string `json:"type,omitempty"`
	Storage     string `json:"storage,omitempty"`      // persistent volume size (e.g. "50Gi")
	StoragePath string `json:"storage_path,omitempty"` // mount path (defaults to /data)
	// EnvVars seeds the app's config store with plain (non-secret) values —
	// shorthand for a SetConfig per key. Use SetConfig to change them afterwards;
	// there is no second env layer on the app itself.
	EnvVars             map[string]string `json:"env_vars,omitempty"`
	ServiceAccount      string            `json:"service_account,omitempty"` // SA email or ID
	HealthCheckPath     string            `json:"health_check_path,omitempty"`
	HealthCheckTimeout  int               `json:"health_check_timeout,omitempty"`
	HealthCheckInterval int               `json:"health_check_interval,omitempty"`
	HealthCheckRetries  int               `json:"health_check_retries,omitempty"`
	Probes              *ProbeOverrides   `json:"probes,omitempty"` // per-probe path/timing overrides (#453); nil = every probe uses the HealthCheck* shorthand
}

// UpdateProbesRequest replaces an app's per-probe liveness/readiness/startup
// overrides (#453). nil clears them, reverting every probe to the shared
// HealthCheck* shorthand.
type UpdateProbesRequest struct {
	Probes *ProbeOverrides `json:"probes"`
}

// DeployRequest is the request body for deploying a new app revision.
type DeployRequest struct {
	Image string `json:"image"`
	// Release names the version this deploy publishes (#471). Optional.
	Release   string `json:"release,omitempty"`
	NoTraffic bool   `json:"no_traffic,omitempty"`
}

// LogsRequest selects what GetAppLogs returns.
//
// Every read is answered from the platform's log store, Follow included, so it
// reaches past the pod that printed the lines and merges every replica
// (ADR-086). Since/Until bound the window and Tail caps how much of it comes
// back; with Follow the window is replayed and then followed, so Until is
// refused — a follow has no far end.
//
// Since and Until each take a Go duration meaning "ago" (e.g. "24h") or an
// RFC3339 timestamp. Empty Since reaches as far back as the store retains;
// empty Until means now. Zero Tail leaves the count to the server, which also
// bounds it.
type LogsRequest struct {
	Follow     bool
	Tail       int
	Since      string
	Until      string
	Timestamps bool
}

// TrafficTarget represents a traffic routing target.
type TrafficTarget struct {
	Revision string `json:"revision"`
	Percent  int64  `json:"percent"`
	URL      string `json:"url,omitempty"`
}

// SetTrafficRequest is the request body for setting traffic split.
type SetTrafficRequest struct {
	Targets []TrafficTarget `json:"targets"`
}

// TrafficResponse is the response for traffic operations.
type TrafficResponse struct {
	Targets []TrafficTarget `json:"targets"`
}

// ScaleRequest is the request body for scaling an app.
type ScaleRequest struct {
	MinScale    *int32 `json:"min_scale,omitempty"`
	MaxScale    *int32 `json:"max_scale,omitempty"`
	Replicas    *int32 `json:"replicas,omitempty"`
	CPULimit    string `json:"cpu_limit,omitempty"`
	MemoryLimit string `json:"memory_limit,omitempty"`
}

// SwitchModeRequest is the request body for switching an app's hosting mode.
type SwitchModeRequest struct {
	Mode string `json:"mode"`
}

// UpdateStorageRequest is the request body for growing an app's persistent storage.
type UpdateStorageRequest struct {
	Storage string `json:"storage"`
}

// SetKubeServiceAccountRequest is the request body for naming the Kubernetes
// ServiceAccount an app's pods run as. Empty clears it back to the hardened
// default (default ServiceAccount, no token mounted).
type SetKubeServiceAccountRequest struct {
	KubeServiceAccount string `json:"kube_service_account"`
}

// UpdateAppRequest is the request body for PATCH /api/v1/apps/{appID}. Both fields
// are optional: display_name changes the app's cosmetic label (the frozen name is
// not renamable in place); url_slug sets or clears the optional vanity host override
// (ADR-040) — a non-nil pointer to "" clears it back to the derived host.
type UpdateAppRequest struct {
	DisplayName string  `json:"display_name,omitempty"`
	URLSlug     *string `json:"url_slug,omitempty"`
	// Database binds the unprefixed DATABASE_URL to one of the project's
	// databases (#544); a pointer to "" clears it back to the default.
	Database *string `json:"database,omitempty"`
}

// UpdateCommandRequest is the request body for changing an app's container
// entrypoint override and arguments. Each field is optional: a nil pointer leaves
// the value untouched, a non-nil pointer (including an empty array) replaces it —
// an empty array clears the override back to the image defaults.
// SetSecurityContextRequest replaces an app's security context. A nil
// SecurityContext clears it back to the platform default.
type SetSecurityContextRequest struct {
	SecurityContext *SecurityContext `json:"security_context"`
}

type UpdateCommandRequest struct {
	Command        *[]string `json:"command,omitempty"`
	Args           *[]string `json:"args,omitempty"`
	ReleaseCommand *[]string `json:"release_command,omitempty"`
}

// UpdateRoutesRequest replaces an app's per-route visibility carve-outs (#501).
// Replace-in-full: an empty list clears every carve-out.
type UpdateRoutesRequest struct {
	Routes []Route `json:"routes"`
}

// RollbackRequest is the request body for rolling back an app to a previous
// release (#471).
type RollbackRequest struct {
	// Release is the release to return to; empty or "prev" means the one before
	// the current version.
	Release string `json:"release,omitempty"`
	// ConfirmMigrations proceeds past the warning that the rollback crosses
	// release commands, which are not reversed.
	ConfirmMigrations bool `json:"confirm_migrations,omitempty"`
}

// AppVersion is what an app is currently running (#471).
type AppVersion struct {
	AppID          string   `json:"app_id"`
	AppName        string   `json:"app_name"`
	Release        string   `json:"release,omitempty"`
	Image          string   `json:"image"`
	ResolvedImage  string   `json:"resolved_image,omitempty"`
	DeploymentID   string   `json:"deployment_id,omitempty"`
	Status         string   `json:"status"`
	Trigger        string   `json:"trigger,omitempty"`
	CommitSHA      string   `json:"commit_sha,omitempty"`
	ReleaseCommand []string `json:"release_command,omitempty"`
	DeployedAt     *string  `json:"deployed_at,omitempty"`
	DeployedBy     string   `json:"deployed_by,omitempty"`
}

// Database represents a managed database instance.
type Database struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Engine      string `json:"engine"`
	Version     string `json:"version"`
	Plan        string `json:"plan"`
	Status      string `json:"status"`
	// Host/Port/Username are the database's address on the cluster network, as
	// recorded at provisioning. Password is returned ONLY on create — CNPG owns
	// the app role and rotates it out of band, so the live credential comes from
	// the injected DATABASE_URL or `fpcloud db connect`, never from this record.
	Host     string `json:"host"`
	Port     int32  `json:"port"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Pooler   bool   `json:"pooler"`
	// CPU/Memory/Storage/Instances are the spec the database is running under —
	// the same four UpdateDatabaseRequest changes. They are read from the live
	// cluster rather than from a stored copy, so they report what is actually
	// running; all four are empty/zero when the cluster cannot be reached.
	CPU       string `json:"cpu,omitempty"`
	Memory    string `json:"memory,omitempty"`
	Storage   string `json:"storage,omitempty"`
	Instances int64  `json:"instances,omitempty"`
	// Extensions names the curated Postgres extensions installed in the
	// database. Untrusted extensions are installed by the platform, because
	// CREATE EXTENSION on one is superuser-only and a managed database hands
	// out no superuser.
	Extensions []string  `json:"extensions"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// DatabaseConnection is a database's live connection info (GET
// /databases/{id}/connection): the real CNPG credentials plus the
// cluster-internal host, reachable through a port-forward tunnel
// (`fpcloud db connect`).
type DatabaseConnection struct {
	ProjectID string `json:"project_id"`
	Namespace string `json:"namespace"`
	Cluster   string `json:"cluster"`
	Host      string `json:"host"`
	Port      int32  `json:"port"`
	Database  string `json:"database"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	URL       string `json:"url"`
}

// CreateDatabaseRequest is the request body for creating a database.
type CreateDatabaseRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Engine      string `json:"engine"`
	Version     string `json:"version,omitempty"`
	CPU         string `json:"cpu,omitempty"`
	Memory      string `json:"memory,omitempty"`
	Storage     string `json:"storage,omitempty"`
	Pooler      bool   `json:"pooler,omitempty"`
	// Extensions names curated extensions to install at create.
	Extensions []string `json:"extensions,omitempty"`
}

// UpdateDatabaseRequest is the request body for reconciling a database's spec.
// Empty strings and nil pointers mean "leave unchanged".
type UpdateDatabaseRequest struct {
	DisplayName string `json:"display_name,omitempty"`
	CPU         string `json:"cpu,omitempty"`
	Memory      string `json:"memory,omitempty"`
	Storage     string `json:"storage,omitempty"`
	Version     string `json:"version,omitempty"`
	Instances   *int64 `json:"instances,omitempty"`
	Pooler      *bool  `json:"pooler,omitempty"`
	// Extensions replaces the installed set; nil leaves it unchanged, an empty
	// list uninstalls what the platform installed.
	Extensions *[]string `json:"extensions,omitempty"`
}

// Bucket is a managed S3 object-storage bucket on the Garage store (ADR-039).
// SecretAccessKey is only populated on creation.
type Bucket struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	Name            string    `json:"name"`
	GarageBucketID  string    `json:"garage_bucket_id,omitempty"`
	AccessKeyID     string    `json:"access_key_id,omitempty"`
	SecretAccessKey string    `json:"secret_access_key,omitempty"`
	GlobalAlias     string    `json:"global_alias,omitempty"`
	Region          string    `json:"region,omitempty"`
	Endpoint        string    `json:"endpoint,omitempty"`
	QuotaMaxSize    int64     `json:"quota_max_size,omitempty"`
	QuotaMaxObjects int64     `json:"quota_max_objects,omitempty"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	// Static-website serving on Garage's s3_web plane (#342). When enabled the
	// bucket is served anonymously over HTTP (public read) at WebsiteURL.
	WebsiteEnabled       bool   `json:"website_enabled"`
	WebsiteIndexDocument string `json:"website_index_document,omitempty"`
	WebsiteErrorDocument string `json:"website_error_document,omitempty"`
	URLSlug              string `json:"url_slug"`
	WebsiteURL           string `json:"website_url,omitempty"`
	WebsiteVersion       int    `json:"website_version"`
}

// SetBucketURLSlugRequest is the request body for setting (or clearing, with
// "") a bucket website's vanity host label.
type SetBucketURLSlugRequest struct {
	URLSlug string `json:"url_slug"`
}

// CreateBucketRequest is the request body for creating a bucket.
type CreateBucketRequest struct {
	Name            string `json:"name"`
	QuotaMaxSize    int64  `json:"quota_max_size,omitempty"`
	QuotaMaxObjects int64  `json:"quota_max_objects,omitempty"`
}

// SetBucketQuotaRequest is the request body for updating a bucket's quotas.
type SetBucketQuotaRequest struct {
	QuotaMaxSize    int64 `json:"quota_max_size"`
	QuotaMaxObjects int64 `json:"quota_max_objects"`
}

// SetBucketWebsiteRequest is the request body for toggling static-website
// serving on a bucket (#342). Enabling makes the bucket world-readable over
// HTTP; the index/error documents are optional (index defaults to index.html).
type SetBucketWebsiteRequest struct {
	Enabled       bool   `json:"enabled"`
	IndexDocument string `json:"index_document,omitempty"`
	ErrorDocument string `json:"error_document,omitempty"`
}

// PublishBucketWebsiteRequest is the request body for atomically flipping a
// website bucket to an already-uploaded version (#439).
type PublishBucketWebsiteRequest struct {
	Version int `json:"version"`
}

// BucketLifecycleRule expires objects on a bucket by age (#498). It is keyed by
// the prefix it applies to (empty = the whole bucket), so one bucket can expire
// derived artefacts under one prefix while everything else is kept forever.
// ExpireDays deletes objects older than N days; AbortIncompleteUploadDays
// reclaims the parts of multipart uploads that were never completed. 0 = not set.
type BucketLifecycleRule struct {
	ID                        string    `json:"id"`
	BucketID                  string    `json:"bucket_id"`
	Prefix                    string    `json:"prefix"`
	ExpireDays                int       `json:"expire_days"`
	AbortIncompleteUploadDays int       `json:"abort_incomplete_upload_days"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

// SetBucketLifecycleRuleRequest upserts the expiry rule for one prefix; other
// prefixes' rules are untouched.
type SetBucketLifecycleRuleRequest struct {
	Prefix                    string `json:"prefix"`
	ExpireDays                int    `json:"expire_days,omitempty"`
	AbortIncompleteUploadDays int    `json:"abort_incomplete_upload_days,omitempty"`
}

// BucketCORSRule is one cross-origin rule on a bucket (#887) — the S3 CORSRule
// shape, which is also what R2 and the Terraform providers for both expose.
//
// It governs what a browser may do against the bucket directly, and only that:
// a request made from a server is not cross-origin and never sends a preflight.
// A presigned url is no substitute — the preflight carries no signature, so the
// browser is refused before authorization is consulted.
//
// The set is ordered and the order matters: the store answers with the first
// rule whose origin matches.
type BucketCORSRule struct {
	ID             string    `json:"id"`
	BucketID       string    `json:"bucket_id"`
	Position       int       `json:"position"`
	AllowedOrigins []string  `json:"allowed_origins"`
	AllowedMethods []string  `json:"allowed_methods"`
	AllowedHeaders []string  `json:"allowed_headers"`
	ExposeHeaders  []string  `json:"expose_headers"`
	MaxAgeSeconds  int       `json:"max_age_seconds"`
	CreatedAt      time.Time `json:"created_at"`
}

// SetBucketCORSRequest replaces a bucket's whole CORS configuration. Whole-set
// rather than per-rule because a rule has no key to address it by, and the
// object store's own PutBucketCors replaces the entire document too. An empty
// list means "allow no cross-origin access".
type SetBucketCORSRequest struct {
	Rules []BucketCORSRuleRequest `json:"rules"`
}

// BucketCORSRuleRequest is one rule in that configuration.
type BucketCORSRuleRequest struct {
	AllowedOrigins []string `json:"allowed_origins"`
	AllowedMethods []string `json:"allowed_methods"`
	AllowedHeaders []string `json:"allowed_headers,omitempty"`
	ExposeHeaders  []string `json:"expose_headers,omitempty"`
	MaxAgeSeconds  int      `json:"max_age_seconds,omitempty"`
}

// BucketKey is a scoped S3 access key for a bucket. SecretAccessKey is only
// populated when the key is created.
type BucketKey struct {
	ID              string    `json:"id"`
	BucketID        string    `json:"bucket_id"`
	AccessKeyID     string    `json:"access_key_id"`
	Name            string    `json:"name,omitempty"`
	CanRead         bool      `json:"can_read"`
	CanWrite        bool      `json:"can_write"`
	CanOwner        bool      `json:"can_owner"`
	SecretAccessKey string    `json:"secret_access_key,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// CreateBucketKeyRequest is the request body for minting a scoped access key.
type CreateBucketKeyRequest struct {
	Name  string `json:"name,omitempty"`
	Read  bool   `json:"read"`
	Write bool   `json:"write"`
	Owner bool   `json:"owner"`
}

// UpdateBucketKeyPermissionsRequest is the request body for changing a key's grants.
type UpdateBucketKeyPermissionsRequest struct {
	Read  bool `json:"read"`
	Write bool `json:"write"`
	Owner bool `json:"owner"`
}

// AppBucketBinding is an explicit app ⇄ bucket binding (#264). Binding injects the
// bucket's S3_*/AWS_* credentials into the app's pod via a k8s Secret + envFrom.
// The secret access key is never returned.
type AppBucketBinding struct {
	AppID       string    `json:"app_id"`
	BucketID    string    `json:"bucket_id"`
	BucketName  string    `json:"bucket_name,omitempty"`
	Endpoint    string    `json:"endpoint,omitempty"`
	Region      string    `json:"region,omitempty"`
	ReadOnly    bool      `json:"read_only"`
	AccessKeyID string    `json:"access_key_id,omitempty"`
	SecretName  string    `json:"secret_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// BindBucketRequest is the request body for binding a bucket to an app.
type BindBucketRequest struct {
	BucketID string `json:"bucket_id"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// BucketCredentials are the S3 connection details for a bucket. SecretAccessKey
// is only present when a fresh key was minted.
type BucketCredentials struct {
	Bucket          string `json:"bucket"`
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	Note            string `json:"note,omitempty"`
}

// BucketSessionCredentials is an expiring S3 credential minted for the caller's
// own data-plane traffic (ADR-074). It carries the caller's own permissions on
// the bucket — CanWrite is false for a caller that may only read — and stops
// working at ExpiresAt without anything having to revoke it.
type BucketSessionCredentials struct {
	Bucket          string    `json:"bucket"`
	Endpoint        string    `json:"endpoint"`
	Region          string    `json:"region"`
	AccessKeyID     string    `json:"access_key_id"`
	SecretAccessKey string    `json:"secret_access_key"`
	CanRead         bool      `json:"can_read"`
	CanWrite        bool      `json:"can_write"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// ObjectInfo is a single stored object in the in-browser object browser (#268).
type ObjectInfo struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"last_modified"`
}

// ObjectListing is one page of a bucket's objects under a prefix; Prefixes are
// the "folder" common-prefixes when a delimiter is used (#268).
type ObjectListing struct {
	Prefixes []string     `json:"prefixes"`
	Objects  []ObjectInfo `json:"objects"`
}

// PresignObjectRequest is the request body for minting a presigned object URL.
type PresignObjectRequest struct {
	Key     string `json:"key"`
	Method  string `json:"method"`            // GET (download) or PUT (upload)
	Expires int    `json:"expires,omitempty"` // seconds; clamped server-side
}

// PresignResponse is a presigned S3 URL the browser uses to GET/PUT an object
// directly against the object store — bytes never transit the API (#268).
type PresignResponse struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
	Expires int               `json:"expires"`
}

// Domain represents a custom domain attached to an application.
type Domain struct {
	ID                string     `json:"id"`
	AppID             string     `json:"app_id,omitempty"`
	BucketID          string     `json:"bucket_id,omitempty"`
	Domain            string     `json:"domain"`
	Mode              string     `json:"mode"`
	Status            string     `json:"status"`
	TLSStatus         string     `json:"tls_status"`
	VerificationToken string     `json:"verification_token,omitempty"`
	VerifiedAt        *time.Time `json:"verified_at,omitempty"`
	// Routes fan the host out to other apps by path prefix (#581, ADR-060);
	// AppID above is the catch-all "/" backend.
	Routes    []DomainRoute `json:"routes,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// DomainRoute sends one path prefix of a hostname to a backend app (#581,
// ADR-060) — the cross-app counterpart to Route, which selects a path's
// visibility within one app. The request path reaches the backend unmodified.
type DomainRoute struct {
	Path    string `json:"path"`               // path prefix, e.g. "/api/"
	AppID   string `json:"app_id"`             // backend app; always-on, same project as the domain
	AppName string `json:"app_name,omitempty"` // joined for display; ignored on write
}

// DomainRequest is the request body for adding or removing a domain.
type DomainRequest struct {
	Domain string `json:"domain"`
	// Mode selects the attachment behavior (ADR-044); empty defaults to "verified".
	Mode string `json:"mode,omitempty"`
}

// SetDomainRoutesRequest replaces a domain's path->app route table (#581).
// Replace-in-full: an empty list clears the fan-out.
type SetDomainRoutesRequest struct {
	Routes []DomainRoute `json:"routes"`
}

// Domain attachment modes (ADR-044).
const (
	DomainModeVerified = "verified"
	DomainModeEdge     = "edge"
	DomainModeOnDemand = "on_demand"
	DomainModeWildcard = "wildcard"
)

// DomainVerification is the ownership/pointing/cert breakdown for a custom
// domain plus the exact DNS records the tenant still needs to configure.
type DomainVerification struct {
	Domain         *Domain `json:"domain"`
	TXTVerified    bool    `json:"txt_verified"`
	DNSPointing    bool    `json:"dns_pointing"`
	CertReady      bool    `json:"cert_ready"`
	CertReason     string  `json:"cert_reason,omitempty"`
	CertExpiry     string  `json:"cert_expiry,omitempty"`
	TXTRecordName  string  `json:"txt_record_name"`
	TXTRecordValue string  `json:"txt_record_value"`
	PointingType   string  `json:"pointing_type"`
	PointingName   string  `json:"pointing_name"`
	PointingValue  string  `json:"pointing_value"`
	// AcmeCNAMEName/AcmeCNAMEValue are the one-time ACME DNS-01 delegation CNAME
	// a wildcard-mode domain must add (ADR-044); empty for every other mode.
	AcmeCNAMEName  string `json:"acme_cname_name,omitempty"`
	AcmeCNAMEValue string `json:"acme_cname_value,omitempty"`
}

// AppConfig represents an environment variable or secret for an application.
type AppConfig struct {
	ID        string    `json:"id"`
	AppID     string    `json:"app_id"`
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	IsSecret  bool      `json:"is_secret"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SetConfigRequest is the request body for setting a config value.
type SetConfigRequest struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret"`
}

// Revision represents a Knative revision for an application.
type Revision struct {
	Name      string `json:"name"`
	Ready     bool   `json:"ready"`
	Image     string `json:"image"`
	CreatedAt string `json:"created_at"`
}

// User represents a registered platform user.
type User struct {
	ID             string    `json:"id"`
	Email          string    `json:"email"`
	Name           string    `json:"name"`
	OrganizationID string    `json:"organization_id"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

// Organization represents a platform organization.
type Organization struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	ShortID     string    `json:"short_id"`
	DisplayName string    `json:"display_name"`
	FKEEnabled  bool      `json:"fke_enabled"` // operator-granted entitlement gating FKE/kubectl access
	CreatedAt   time.Time `json:"created_at"`
}

// UpdateOrgRequest is the request body for updating an organization. FKEEnabled is
// a pointer so an omitted field is distinguishable from an explicit false;
// DisplayName changes the mutable cosmetic label.
type UpdateOrgRequest struct {
	DisplayName string `json:"display_name,omitempty"`
}

// SetOrgFKERequest toggles the FKE entitlement. Operator-only: it targets
// PUT /admin/orgs/{id}/fke, not the tenant PATCH (#710). Pointer so an omitted
// field is refused rather than read as "disable".
type SetOrgFKERequest struct {
	Enabled *bool `json:"enabled"`
}

// OrgSecret is a Fogpipe Secrets Manager bundle (ADR-028): an org-scoped named
// set of key/value entries. Data is populated only on an explicit reveal.
type OrgSecret struct {
	ID        string            `json:"id"`
	OrgID     string            `json:"org_id"`
	Name      string            `json:"name"`
	Keys      []string          `json:"keys"`
	Data      map[string]string `json:"data,omitempty"`
	Targets   []string          `json:"targets"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// SetupWebhookRequest is the request body for setting up a webhook.
type SetupWebhookRequest struct {
	Repo         string `json:"repo"`
	Branch       string `json:"branch"`
	ImagePattern string `json:"image_pattern"`
}

// AppWebhook represents a webhook configuration (returned from setup).
type AppWebhook struct {
	ID            string  `json:"id"`
	AppID         string  `json:"app_id"`
	Provider      string  `json:"provider"`
	Repo          string  `json:"repo"`
	Branch        string  `json:"branch"`
	ImagePattern  string  `json:"image_pattern"`
	Enabled       bool    `json:"enabled"`
	WebhookURL    string  `json:"webhook_url"`
	WebhookSecret string  `json:"webhook_secret,omitempty"`
	LastDeployAt  *string `json:"last_deploy_at,omitempty"`
	LastDeploySHA string  `json:"last_deploy_sha,omitempty"`
}

// RegisterResponse is the response from provisioning a user account.
type RegisterResponse struct {
	User         *User         `json:"user"`
	Organization *Organization `json:"organization"`
	APIKey       string        `json:"api_key"`
}

// ProvisionUserRequest is the request body for admin-provisioning a user
// into an existing organization (POST /api/v1/orgs/{orgID}/users).
type ProvisionUserRequest struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role,omitempty"`
}

// MeResponse is the response from the /auth/me endpoint.
type MeResponse struct {
	User         *User         `json:"user"`
	Organization *Organization `json:"organization"`
}

// ServiceAccount represents a service account.
type ServiceAccount struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateServiceAccountRequest is the request body for creating a service account.
type CreateServiceAccountRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
}

// UpdateServiceAccountRequest is the request body for updating a service account's
// mutable cosmetic display name.
type UpdateServiceAccountRequest struct {
	DisplayName string `json:"display_name,omitempty"`
}

// ServiceAccountKey represents a service account key.
type ServiceAccountKey struct {
	ID               string  `json:"id"`
	ServiceAccountID string  `json:"service_account_id"`
	APIKey           string  `json:"api_key,omitempty"`
	Prefix           string  `json:"prefix"`
	CreatedAt        string  `json:"created_at"`
	ExpiresAt        *string `json:"expires_at,omitempty"`
}

// IAMBinding represents an IAM role binding.
type IAMBinding struct {
	ID           string `json:"id"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Role         string `json:"role"`
	MemberType   string `json:"member_type"`
	Member       string `json:"member"`
	CreatedAt    string `json:"created_at"`
}

// OrgMember represents a member of an organization.
type OrgMember struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	UserID         string `json:"user_id"`
	Role           string `json:"role"`
	InvitedBy      string `json:"invited_by,omitempty"`
	InvitedEmail   string `json:"invited_email,omitempty"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	UserEmail      string `json:"user_email,omitempty"`
	UserName       string `json:"user_name,omitempty"`
}

// InviteOrgMemberRequest is the request body for inviting a member to an organization.
type InviteOrgMemberRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// UpdateOrgMemberRoleRequest is the request body for updating a member's role.
type UpdateOrgMemberRoleRequest struct {
	Role string `json:"role"`
}

// DatabaseBackup represents a backup of a managed database.
type DatabaseBackup struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	Method    string `json:"method,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	StoppedAt string `json:"stopped_at,omitempty"`
}

// BackupConfig represents the backup configuration for a database, plus what the
// cluster has actually done with it (Problems).
type BackupConfig struct {
	Enabled                  bool   `json:"enabled"`
	Schedule                 string `json:"schedule"`
	Retention                string `json:"retention"`
	FirstRecoverabilityPoint string `json:"first_recoverability_point,omitempty"`
	// RecoverableTo is the newest point a restore can reach — the ceiling of the
	// window whose floor is FirstRecoverabilityPoint. It is the read time while
	// WAL is reaching the archive, and the moment archiving broke while it is
	// not. Reporting only the floor made a window with a 32h hole in it read as
	// continuous (#896).
	RecoverableTo string `json:"recoverable_to,omitempty"`
	// Archiving reports whether WAL is currently reaching the archive, which is
	// what decides whether RecoverableTo is still advancing or frozen.
	Archiving bool `json:"archiving"`
	// Healthy is the verdict, stated rather than inferred. It used to be derived
	// client-side from len(Problems)==0, which meant an old server, a dropped
	// key and a working database were the same bytes to a monitor (#896).
	Healthy bool `json:"healthy"`
	// Problems are the reasons this database's backups are not producing restore
	// points, derived from live cluster state on every read. Always serialised —
	// `[]` when there are none — so absence never has to be read as health.
	Problems []BackupProblem `json:"problems"`
}

// BackupProblem is one reason a database's backups are not producing restore
// points: a backup wedged mid-run ("backup-stuck"), a newest attempt that failed
// ("backup-failing"), a schedule the operator stopped firing
// ("schedule-overdue"), or one whose cluster is gone ("schedule-orphaned").
type BackupProblem struct {
	Object string `json:"object"`
	Reason string `json:"reason"`
	Detail string `json:"detail"`
	Since  string `json:"since,omitempty"`
}

// SetBackupDestinationRequest is what configures a database's external backup
// target. It is a separate type from BackupDestination on purpose: that one is
// the server's answer and carries fields the server owns (`enabled`, the last
// run), which a write neither sets nor is allowed to send.
type SetBackupDestinationRequest struct {
	Provider        string `json:"provider"` // "aws" | "gcp" | "s3"
	Bucket          string `json:"bucket"`
	Region          string `json:"region,omitempty"`
	Prefix          string `json:"prefix,omitempty"`
	FlatLayout      bool   `json:"flat_layout,omitempty"` // skip the <project>/<database> nesting under prefix
	RoleARN         string `json:"role_arn,omitempty"`
	WIFProvider     string `json:"wif_provider,omitempty"`
	ServiceAccount  string `json:"service_account,omitempty"`
	Audience        string `json:"audience,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`          // s3
	AccessKeyID     string `json:"access_key_id,omitempty"`     // s3
	SecretAccessKey string `json:"secret_access_key,omitempty"` // s3 (write-only)
	Schedule        string `json:"schedule,omitempty"`
}

// UpdateBackupConfigRequest is what turns managed backups on or off and sets
// their schedule and retention. Separate from BackupConfig for the same reason:
// the read carries derived state (the recoverability point, the problems) that
// is the server's to report and nobody's to send.
type UpdateBackupConfigRequest struct {
	Enabled   bool   `json:"enabled"`
	Schedule  string `json:"schedule,omitempty"`
	Retention string `json:"retention,omitempty"`
}

// BackupDestination is an opt-in, per-database external backup target (issue #130,
// #394): the customer's own bucket the database backs up directly to. Two auth
// models — keyless via OIDC federation (provider "aws" RoleARN, "gcp" WIFProvider
// + ServiceAccount), or a static S3 key (provider "s3": Endpoint + AccessKeyID +
// SecretAccessKey) for any S3-compatible store (Cloudflare R2, Backblaze B2,
// Hetzner Object Storage, Garage). SecretAccessKey is write-only — set it, but it
// is never returned; omit on update to keep the stored one.
type BackupDestination struct {
	Provider        string `json:"provider"` // "aws" | "gcp" | "s3"
	Bucket          string `json:"bucket"`
	Region          string `json:"region,omitempty"`
	Prefix          string `json:"prefix,omitempty"`
	FlatLayout      bool   `json:"flat_layout,omitempty"` // skip the <project>/<database> nesting under prefix
	RoleARN         string `json:"role_arn,omitempty"`
	WIFProvider     string `json:"wif_provider,omitempty"`
	ServiceAccount  string `json:"service_account,omitempty"`
	Audience        string `json:"audience,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`          // s3
	AccessKeyID     string `json:"access_key_id,omitempty"`     // s3
	SecretAccessKey string `json:"secret_access_key,omitempty"` // s3 (write-only)
	Schedule        string `json:"schedule,omitempty"`
	Enabled         bool   `json:"enabled"`
	LastRunAt       string `json:"last_run_at,omitempty"`
	LastRunStatus   string `json:"last_run_status,omitempty"`
}

// BackupDestinationRun identifies an on-demand external backup that was started.
// The backup runs as an async k8s Job; Status reflects the launch, not completion.
type BackupDestinationRun struct {
	JobName string `json:"job_name"`
	Status  string `json:"status"`
	Subject string `json:"subject"`
}

// RestoreRequest is the request body for restoring a database from backup.
type RestoreRequest struct {
	PointInTime string `json:"point_in_time,omitempty"`
	TargetName  string `json:"target_name"`
}

// Deployment represents a single deployment event for an application.
type Deployment struct {
	ID    string `json:"id"`
	AppID string `json:"app_id"`
	Image string `json:"image"`
	// Release is the user-named release this deploy published (#471);
	// ResolvedImage the digest-pinned reference it actually ran.
	Release        string   `json:"release,omitempty"`
	ResolvedImage  string   `json:"resolved_image,omitempty"`
	ReleaseCommand []string `json:"release_command,omitempty"`
	Status         string   `json:"status"`
	Trigger        string   `json:"trigger"`
	CommitSHA      string   `json:"commit_sha,omitempty"`
	Message        string   `json:"message,omitempty"`
	// ReleaseLogs is the release-command Job's output for this deploy, read by the
	// API from the log store rather than stored on the record. Empty once the
	// store's retention has passed, or before it has ingested the last lines.
	ReleaseLogs string  `json:"release_logs,omitempty"`
	StartedAt   string  `json:"started_at"`
	FinishedAt  *string `json:"finished_at,omitempty"`
	DurationMs  *int    `json:"duration_ms,omitempty"`
	CreatedBy   string  `json:"created_by,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

// SetIAMBindingRequest is the request body for setting an IAM binding.
type SetIAMBindingRequest struct {
	Role       string `json:"role"`
	MemberType string `json:"member_type"`
	Member     string `json:"member"`
}

// APIError represents an error response from the API.
// It supports both the new nested format {"error":{"code":"...","message":"..."}}
// and the legacy flat format {"error":"message"}.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

// UnmarshalJSON implements custom JSON unmarshaling to handle both the new
// nested error format and the legacy flat format.
func (e *APIError) UnmarshalJSON(data []byte) error {
	// Try nested format: {"error": {"code": "...", "message": "..."}}
	var nested struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &nested); err == nil && nested.Error.Message != "" {
		e.Code = nested.Error.Code
		e.Message = nested.Error.Message
		return nil
	}

	// Try flat format: {"error": "message"}
	var flat struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &flat); err == nil && flat.Error != "" {
		e.Message = flat.Error
		return nil
	}

	// Try message format: {"message": "..."}
	var msg struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &msg); err == nil && msg.Message != "" {
		e.Message = msg.Message
		return nil
	}

	return fmt.Errorf("unknown error format")
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

// ErrNotFound is a sentinel matched via errors.Is against an *APIError with a 404
// status. It lets callers branch on "the API doesn't know this route/resource"
// — e.g. the CLI falling back to embedded cluster constants against an older API
// that lacks the FKE credentials endpoint.
var ErrNotFound = errors.New("not found")

// ErrClientTooOld is a sentinel matched via errors.Is against an *APIError with
// a 426 status: this client is older than the deployment serves, and no request
// it makes will be answered until it is upgraded. Every other reading of the
// same failure — a bad flag, a malformed body, a missing resource — is wrong,
// which is the whole reason the deployment states a minimum rather than letting
// the request fail on its own terms.
var ErrClientTooOld = errors.New("client too old")

// Is reports whether target is one of this package's sentinels and this error
// carries the matching status, so errors.Is works on responses from do().
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return e.StatusCode == http.StatusNotFound
	case ErrClientTooOld:
		return e.StatusCode == http.StatusUpgradeRequired
	}
	return false
}

// ClusterCredentials is the cluster connection facts for assembling a kubeconfig
// context (GET /projects/{id}/fke/credentials). Server + CertificateAuthorityData
// are cluster-global; Context/Namespace are per-project. The bearer token is
// minted separately by the exec plugin (FKEToken).
type ClusterCredentials struct {
	Server                   string `json:"server"`
	CertificateAuthorityData string `json:"certificate_authority_data"`
	Context                  string `json:"context"`
	Namespace                string `json:"namespace"`
}

// ClusterToken is a short-lived, namespace-scoped Kubernetes token bound to the
// project's ServiceAccount (POST /projects/{id}/fke/token).
type ClusterToken struct {
	Token               string `json:"token"`
	ExpirationTimestamp string `json:"expiration_timestamp"`
}

// ClusterInfo is the project-independent cluster connection facts (GET
// /cluster-info): the apiserver URL and CA bundle, both public information (they
// appear in every kubeconfig). Used by the staff cluster-admin path, which is not
// project-scoped, so the CLI binary carries no baked-in cluster endpoint/CA.
type ClusterInfo struct {
	Server                   string `json:"server"`
	CertificateAuthorityData string `json:"certificate_authority_data"`
}

// Job is a scheduled task within a project (#166): the recipe plus when to run
// it. Either it runs a container image or it sends an HTTP request; referencing
// an app makes it inherit that app's image, config and identity.
type Job struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	AppID       string `json:"app_id,omitempty"`
	AppName     string `json:"app_name,omitempty"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`

	Schedule    string `json:"schedule"`
	Timezone    string `json:"timezone"`
	Concurrency string `json:"concurrency"`
	MaxRetries  int    `json:"max_retries"`
	Timeout     int    `json:"timeout_seconds"`
	KeepRuns    int    `json:"keep_runs"`
	// RetainSucceeded and RetainFailed age out run history per outcome, on top
	// of KeepRuns; 0 means that outcome has no age bound.
	RetainSucceeded int  `json:"retain_succeeded_seconds"`
	RetainFailed    int  `json:"retain_failed_seconds"`
	Suspended       bool `json:"suspended"`

	Target      string            `json:"target"`
	Image       string            `json:"image,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	HTTPURL     string            `json:"http_url,omitempty"`
	HTTPMethod  string            `json:"http_method,omitempty"`
	HTTPHeaders map[string]string `json:"http_headers,omitempty"`
	HTTPBody    string            `json:"http_body,omitempty"`

	LastRun *JobRun `json:"last_run,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// JobRun is one execution of a job — a record, not a declarable resource.
type JobRun struct {
	ID         string     `json:"id"`
	JobID      string     `json:"job_id"`
	RunName    string     `json:"run_name"`
	Trigger    string     `json:"trigger"`
	Status     string     `json:"status"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	DurationMs *int       `json:"duration_ms,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Job target types.
const (
	JobTargetContainer = "container"
	JobTargetHTTP      = "http"
)

// CreateJobRequest is the request body for creating a scheduled job.
type CreateJobRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	App         string `json:"app,omitempty"`
	Schedule    string `json:"schedule"`
	Timezone    string `json:"timezone,omitempty"`
	Concurrency string `json:"concurrency,omitempty"`
	MaxRetries  *int   `json:"max_retries,omitempty"`
	Timeout     *int   `json:"timeout_seconds,omitempty"`
	KeepRuns    *int   `json:"keep_runs,omitempty"`

	RetainSucceeded *int `json:"retain_succeeded_seconds,omitempty"`
	RetainFailed    *int `json:"retain_failed_seconds,omitempty"`
	Suspended       bool `json:"suspended,omitempty"`

	Image       string            `json:"image,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	HTTPURL     string            `json:"http_url,omitempty"`
	HTTPMethod  string            `json:"http_method,omitempty"`
	HTTPHeaders map[string]string `json:"http_headers,omitempty"`
	HTTPBody    string            `json:"http_body,omitempty"`
}

// UpdateJobRequest patches a job; a nil field is left unchanged. Identity
// (project, name, app) is immutable.
type UpdateJobRequest struct {
	DisplayName *string `json:"display_name,omitempty"`
	Schedule    *string `json:"schedule,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
	Concurrency *string `json:"concurrency,omitempty"`
	MaxRetries  *int    `json:"max_retries,omitempty"`
	Timeout     *int    `json:"timeout_seconds,omitempty"`
	KeepRuns    *int    `json:"keep_runs,omitempty"`

	RetainSucceeded *int               `json:"retain_succeeded_seconds,omitempty"`
	RetainFailed    *int               `json:"retain_failed_seconds,omitempty"`
	Suspended       *bool              `json:"suspended,omitempty"`
	Image           *string            `json:"image,omitempty"`
	Command         *[]string          `json:"command,omitempty"`
	Args            *[]string          `json:"args,omitempty"`
	HTTPURL         *string            `json:"http_url,omitempty"`
	HTTPMethod      *string            `json:"http_method,omitempty"`
	HTTPHeaders     *map[string]string `json:"http_headers,omitempty"`
	HTTPBody        *string            `json:"http_body,omitempty"`
}

// --- Managed GitHub Actions runners (#418, ADR-064) ---

// Runner is a managed GitHub Actions runner pool: a declaration the platform
// turns into ephemeral pods, one per job, in the project's namespace.
type Runner struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`

	GitHubConfigURL string `json:"github_config_url"`
	RunnerGroup     string `json:"runner_group"`
	MinRunners      int    `json:"min_runners"`
	MaxRunners      int    `json:"max_runners"`
	Image           string `json:"image,omitempty"`
	CPU             string `json:"cpu,omitempty"`
	Memory          string `json:"memory,omitempty"`

	// Builder is the image builder that runs alongside each job, or nil for a
	// pool that builds nothing (ADR-071). CPU and Memory bound the builder, not
	// the runner — a read always fills them in, so the pod's cost is the sum of
	// two numbers you can see.
	Builder *RunnerBuilder `json:"builder,omitempty"`

	// Credential is which source the pool authenticates with: platform (the
	// Fogpipe GitHub App), app (your own), or token.
	Credential string `json:"credential"`

	// The private key and the token are write-only and never come back.
	GitHubAppID             string `json:"github_app_id,omitempty"`
	GitHubAppInstallationID string `json:"github_app_installation_id,omitempty"`

	// Labels is what a workflow puts in `runs-on`.
	Labels []string `json:"labels,omitempty"`

	Status         string `json:"status,omitempty"`
	CurrentRunners int    `json:"current_runners,omitempty"`
	Message        string `json:"message,omitempty"`

	// Problems are failures on the pool's own pods — a runner killed for
	// exceeding its memory above all. They do not make the pool unhealthy: the
	// controller replaces the pod, so Status stays `running` while the job that
	// was on it is the thing that died.
	Problems []StatusProblem `json:"problems,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateRunnerRequest is the request body for declaring a runner pool.
//
// It names no GitHub account with the default "platform" credential: the
// account is the one the project connected and proved it controls (#790).
// GitHubAccount applies only to a tenant-supplied credential, which carries no
// account of its own.
type CreateRunnerRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`

	GitHubAccount string `json:"github_account,omitempty"`
	RunnerGroup   string `json:"runner_group,omitempty"`
	MinRunners    *int   `json:"min_runners,omitempty"`
	MaxRunners    *int   `json:"max_runners,omitempty"`
	Image         string `json:"image,omitempty"`
	CPU           string `json:"cpu,omitempty"`
	Memory        string `json:"memory,omitempty"`

	// Builder asks for an image builder alongside each job. Omit it for a pool
	// that builds nothing; an empty value takes the platform's defaults.
	Builder *RunnerBuilder `json:"builder,omitempty"`

	// Credential defaults to "platform" — the Fogpipe GitHub App, installed in
	// one click, with nothing else to supply.
	Credential              string `json:"credential,omitempty"`
	GitHubAppID             string `json:"github_app_id,omitempty"`
	GitHubAppInstallationID string `json:"github_app_installation_id,omitempty"`
	GitHubAppPrivateKey     string `json:"github_app_private_key,omitempty"`
	GitHubToken             string `json:"github_token,omitempty"`
}

// UpdateRunnerRequest patches a runner pool; a nil field is left unchanged.
// Identity (project, name) is immutable.
type UpdateRunnerRequest struct {
	DisplayName   *string `json:"display_name,omitempty"`
	GitHubAccount *string `json:"github_account,omitempty"`
	RunnerGroup   *string `json:"runner_group,omitempty"`
	MinRunners    *int    `json:"min_runners,omitempty"`
	MaxRunners    *int    `json:"max_runners,omitempty"`
	Image         *string `json:"image,omitempty"`
	CPU           *string `json:"cpu,omitempty"`
	Memory        *string `json:"memory,omitempty"`

	Credential              *string `json:"credential,omitempty"`
	GitHubAppID             *string `json:"github_app_id,omitempty"`
	GitHubAppInstallationID *string `json:"github_app_installation_id,omitempty"`
	GitHubAppPrivateKey     *string `json:"github_app_private_key,omitempty"`
	GitHubToken             *string `json:"github_token,omitempty"`
}

// RunnerBuilder is the rootless image builder a pool runs alongside each job
// (ADR-064), and what it costs (ADR-071).
//
// The runner and the builder are two processes with unrelated appetites — the
// runner's memory follows the workflow's steps, the builder's follows the
// Dockerfile — so the builder carries its own sizing rather than inheriting the
// pool's. An unset field takes the platform's default for a builder, which is
// not the pool's own size.
type RunnerBuilder struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// UpdateRunnerBuilderRequest replaces a pool's builder. nil removes it, so the
// pod goes back to a single container.
//
// Its own request rather than a field on UpdateRunnerRequest, matching
// UpdateProbesRequest: a patch leaves an omitted field unchanged, which leaves
// no way to say "remove".
type UpdateRunnerBuilderRequest struct {
	Builder *RunnerBuilder `json:"builder"`
}

// GitHubConnection is the GitHub account a project has proved it controls
// (#790). Runner pools take their scope from it, so there is no organization to
// name when creating one.
type GitHubConnection struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`

	InstallationID string `json:"installation_id"`
	AccountLogin   string `json:"account_login"`
	AccountType    string `json:"account_type"`
	ConnectedBy    string `json:"connected_by,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GitHubConnectStart is where to send someone to install the Fogpipe GitHub App
// and authorize the connection. The URL is single-use in effect: it carries a
// signed, short-lived state naming the project.
type GitHubConnectStart struct {
	URL string `json:"url"`
}

// ProjectStatus is the whole project in one document (GET
// /projects/{id}/status) — every resource kind with its derived status and the
// problems attached to the resource they belong to.
type ProjectStatus struct {
	Project   StatusProject    `json:"project"`
	Apps      []AppStatus      `json:"apps"`
	Databases []DatabaseStatus `json:"databases"`
	Jobs      []JobStatus      `json:"jobs"`
	Domains   []DomainStatus   `json:"domains"`
	Buckets   []BucketStatus   `json:"buckets"`
	Runners   []RunnerStatus   `json:"runners"`

	// Unchecked names the checks that could not be run. A report carrying these
	// is incomplete, not clean — never render it as healthy.
	Unchecked []UncheckedStatus `json:"unchecked,omitempty"`

	ObservedAt time.Time `json:"observed_at"`
}

// StatusProject is the project itself and the caps its namespace is held to.
type StatusProject struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	DisplayName    string `json:"display_name"`
	Namespace      string `json:"namespace"`
	Status         string `json:"status"`
	Egress         string `json:"egress"`
	MaxCPU         string `json:"max_cpu,omitempty"`
	MaxMemory      string `json:"max_memory,omitempty"`
	MaxPods        int    `json:"max_pods,omitempty"`
	MaxStorage     string `json:"max_storage,omitempty"`
}

// StatusProblem is one thing wrong with one resource, in the same shape
// whatever kind it belongs to.
type StatusProblem struct {
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
	Count  int32  `json:"count,omitempty"`
	Since  string `json:"since,omitempty"`
}

// UncheckedStatus is a check that did not run, and why.
type UncheckedStatus struct {
	Check string `json:"check"`
	Error string `json:"error"`
}

// AppStatus is one app: what it should be running, what it is running, and what
// is wrong with it.
type AppStatus struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Mode    string `json:"mode"`
	Image   string `json:"image,omitempty"`
	Release string `json:"release,omitempty"`
	URL     string `json:"url,omitempty"`
	Status  string `json:"status"`
	Desired int32  `json:"desired"`
	Ready   int32  `json:"ready"`
	// RunningImage and RunningRelease are what the cluster's workload declares,
	// which differs from Image/Release above exactly while a deploy is in flight.
	RunningImage   string `json:"running_image,omitempty"`
	RunningRelease string `json:"running_release,omitempty"`
	// Rollout is present only while the app is mid-deploy — its presence is the
	// answer to "is this changing right now".
	Rollout *RolloutStatus `json:"rollout,omitempty"`
	// Pods is the app's population by state: running, coming up, going away.
	Pods *PodPhases `json:"pods,omitempty"`
	// Config is how much configuration the app carries — counts, never values.
	Config   *ConfigCount    `json:"config,omitempty"`
	Problems []StatusProblem `json:"problems,omitempty"`
}

// ConfigCount is how many config values an app holds, and how many are secret.
type ConfigCount struct {
	Values  int `json:"values"`
	Secrets int `json:"secrets"`
}

// PodPhases is how many of an app's pods are running, starting and terminating,
// and how long each has been in that state.
type PodPhases struct {
	Running     int32 `json:"running"`
	Starting    int32 `json:"starting"`
	Terminating int32 `json:"terminating"`
	// RunningSeconds is the age of the oldest running pod — how long this
	// version has actually been serving.
	RunningSeconds     int64 `json:"running_seconds,omitempty"`
	StartingSeconds    int64 `json:"starting_seconds,omitempty"`
	TerminatingSeconds int64 `json:"terminating_seconds,omitempty"`
}

// RolloutStatus is an app mid-deploy: how many replicas are on the new template,
// how many exist in total (old ones included), and what it is waiting for.
type RolloutStatus struct {
	Desired   int32  `json:"desired"`
	Updated   int32  `json:"updated"`
	Total     int32  `json:"total"`
	Available int32  `json:"available"`
	Reason    string `json:"reason"`
}

// DatabaseStatus is one managed database and the state of its restore points.
type DatabaseStatus struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Engine   string          `json:"engine"`
	Version  string          `json:"version"`
	Status   string          `json:"status"`
	Pooler   bool            `json:"pooler"`
	Problems []StatusProblem `json:"problems,omitempty"`
}

// JobStatus is one scheduled job and its most recent run.
type JobStatus struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Schedule  string        `json:"schedule"`
	Timezone  string        `json:"timezone,omitempty"`
	Target    string        `json:"target"`
	Suspended bool          `json:"suspended"`
	LastRun   *JobRunStatus `json:"last_run,omitempty"`
}

// JobRunStatus is the outcome of one run, trimmed to what a status line shows.
type JobRunStatus struct {
	Status     string     `json:"status"`
	Trigger    string     `json:"trigger,omitempty"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	DurationMs *int       `json:"duration_ms,omitempty"`
}

// DomainStatus is one hostname the project serves — the app's own platform host
// included, not only custom ones.
type DomainStatus struct {
	Domain string `json:"domain"`
	// Source is "custom" for a hostname someone attached, "platform" for the one
	// the app was given.
	Source    string          `json:"source"`
	Mode      string          `json:"mode,omitempty"`
	Status    string          `json:"status"`
	TLSStatus string          `json:"tls_status,omitempty"`
	Owner     string          `json:"owner,omitempty"`
	OwnerKind string          `json:"owner_kind,omitempty"`
	Problems  []StatusProblem `json:"problems,omitempty"`
}

// BucketStatus is one managed bucket, and whether it serves a website.
type BucketStatus struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	WebsiteEnabled bool   `json:"website_enabled"`
	WebsiteURL     string `json:"website_url,omitempty"`
}

// RunnerStatus is one CI runner pool and how many runners are alive in it.
type RunnerStatus struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	CurrentRunners int    `json:"current_runners"`
	MinRunners     int    `json:"min_runners"`
	MaxRunners     int    `json:"max_runners"`
	Message        string `json:"message,omitempty"`
}

// AlertEpisode is one continuous stretch of one alert firing, as the control
// plane derives it from Prometheus's ALERTS series.
//
// Nothing stores this: Alertmanager keeps no history and Slack keeps only what
// was delivered, so an episode is reconstructed per request from the samples
// Prometheus wrote on each rule evaluation.
type AlertEpisode struct {
	Alert    string `json:"alert"`
	Severity string `json:"severity,omitempty"`
	// Namespace and Target locate what fired. Empty means the rule carried no
	// such label — an alert about the node or the cluster is scoped to neither.
	Namespace string            `json:"namespace,omitempty"`
	Target    string            `json:"target,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`

	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	// Firing means the episode was still open when observation stopped, so
	// EndedAt is the end of the window and not where the alert cleared.
	Firing bool `json:"firing"`
}

// AlertHistory is what fired over one window.
//
// From/To describe the observation rather than the request: a window longer than
// Prometheus retention comes back as the part that still exists, which is what
// keeps an empty answer from reading as "this never fired".
type AlertHistory struct {
	From        time.Time      `json:"from"`
	To          time.Time      `json:"to"`
	StepSeconds int            `json:"step_seconds"`
	Episodes    []AlertEpisode `json:"episodes"`
}
