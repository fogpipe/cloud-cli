package client

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// IdentityTokenFileEnv names the file an app's pod finds its identity token
// in: a Kubernetes ServiceAccount token the platform projects into every pod
// it renders for the app, with audience "fpcloud", rotated by the kubelet
// (ADR-285). The platform sets the variable; nothing else does.
const IdentityTokenFileEnv = "FPCLOUD_IDENTITY_TOKEN_FILE"

// IdentityTokenFile is the path the environment names, or empty outside a pod.
func IdentityTokenFile() string {
	return os.Getenv(IdentityTokenFileEnv)
}

// TokenSource hands the client a bearer credential per request. A Client with
// an APIKey never consults it.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

const (
	grantTokenExchange = "urn:ietf:params:oauth:grant-type:token-exchange"
	tokenTypeJWT       = "urn:ietf:params:oauth:token-type:jwt"
)

// TokenExchangeRequest is the body of POST /api/v1/auth/token (RFC 8693).
type TokenExchangeRequest struct {
	GrantType        string `json:"grant_type"`
	SubjectToken     string `json:"subject_token"`
	SubjectTokenType string `json:"subject_token_type"`
	// Audience names the service account to assume (id or email). A pod's
	// token names its app, so a pod leaves it empty; a CI token matched by a
	// trust binding may name one of several accounts the binding authorizes.
	Audience string `json:"audience,omitempty"`
}

// TokenExchangeResponse is the minted credential: a bearer key the platform
// expires after ExpiresIn seconds.
type TokenExchangeResponse struct {
	AccessToken     string `json:"access_token"`
	TokenType       string `json:"token_type"`
	ExpiresIn       int    `json:"expires_in"`
	IssuedTokenType string `json:"issued_token_type"`
}

// ExchangeToken trades a subject token — a pod's projected identity token, or
// a CI provider's OIDC token — for a short-lived platform credential.
// Unauthenticated: the subject token is the proof.
func (c *Client) ExchangeToken(ctx context.Context, subjectToken, audience string) (*TokenExchangeResponse, error) {
	body := TokenExchangeRequest{
		GrantType:        grantTokenExchange,
		SubjectToken:     subjectToken,
		SubjectTokenType: tokenTypeJWT,
		Audience:         audience,
	}
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/auth/token", body)
	if err != nil {
		return nil, err
	}
	// The exchange is what obtains a credential, so it must not wait on one.
	httpReq.Header.Del("Authorization")
	var resp TokenExchangeResponse
	if err := c.do(httpReq, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// identityRefreshMargin is how long before the minted credential expires the
// next request exchanges again. The platform mints for 15 minutes; a margin of
// a minute keeps a request that starts just before expiry from carrying a key
// the platform has already stopped honouring.
const identityRefreshMargin = time.Minute

// PodIdentity is a TokenSource for a process running in an app's pod. It reads
// the projected token from the file, exchanges it for a platform credential,
// and reuses that credential until it is about to expire, then exchanges
// again. The file is re-read on every exchange, because the kubelet rotates
// it; the token is read once per exchange, not once per request.
type PodIdentity struct {
	exchange *Client
	path     string
	now      func() time.Time

	mu      sync.Mutex
	key     string
	expires time.Time
}

// NewPodIdentity is a TokenSource exchanging the token at path against the
// API at baseURL.
func NewPodIdentity(baseURL, path string) *PodIdentity {
	return &PodIdentity{exchange: New(baseURL, ""), path: path, now: time.Now}
}

// NewWithPodIdentity is a Client for a process in an app's pod: no key, the
// pod's identity token exchanged on demand (ADR-285). The CLI and the
// provider build this when FPCLOUD_IDENTITY_TOKEN_FILE is set and nothing
// above it in their credential chain is.
func NewWithPodIdentity(baseURL, path string) *Client {
	c := New(baseURL, "")
	c.TokenSource = NewPodIdentity(baseURL, path)
	return c
}

// Token returns the current platform credential, exchanging for a new one
// when none is held or the held one is within the refresh margin of expiry.
// Concurrent callers share one exchange.
func (p *PodIdentity) Token(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.key != "" && p.now().Add(identityRefreshMargin).Before(p.expires) {
		return p.key, nil
	}
	raw, err := os.ReadFile(p.path)
	if err != nil {
		return "", fmt.Errorf("reading the identity token %s: %w", p.path, err)
	}
	subject := strings.TrimSpace(string(raw))
	if subject == "" {
		return "", fmt.Errorf("the identity token %s is empty", p.path)
	}
	resp, err := p.exchange.ExchangeToken(ctx, subject, "")
	if err != nil {
		return "", fmt.Errorf("exchanging the identity token: %w", err)
	}
	p.key = resp.AccessToken
	p.expires = p.now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	return p.key, nil
}
