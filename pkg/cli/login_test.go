package cli

import (
	"context"
	"net"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// A public PKCE client (self-hosted IdP, ADR-022) has only a client id: no secret
// is sent and client_id goes in the request body.
func TestOAuthConfig_PublicClientWhenNoSecret(t *testing.T) {
	t.Setenv("FPCLOUD_OIDC_CLIENT_ID", "public-client")
	t.Setenv("FPCLOUD_OIDC_CLIENT_SECRET", "")

	conf, err := oauthConfig(context.Background(), "http://127.0.0.1:1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conf.ClientSecret != "" {
		t.Errorf("expected empty client secret, got %q", conf.ClientSecret)
	}
	if conf.Endpoint.AuthStyle != oauth2.AuthStyleInParams {
		t.Errorf("expected AuthStyleInParams for a public client, got %v", conf.Endpoint.AuthStyle)
	}
}

// The Google desktop-app (confidential) path is unchanged: the secret is preserved
// and auth style stays auto-detected.
func TestOAuthConfig_ConfidentialClientKeepsSecret(t *testing.T) {
	t.Setenv("FPCLOUD_OIDC_CLIENT_ID", "conf-client")
	t.Setenv("FPCLOUD_OIDC_CLIENT_SECRET", "shh")

	conf, err := oauthConfig(context.Background(), "http://127.0.0.1:1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conf.ClientSecret != "shh" {
		t.Errorf("expected secret preserved, got %q", conf.ClientSecret)
	}
	if conf.Endpoint.AuthStyle != oauth2.AuthStyleAutoDetect {
		t.Errorf("expected AuthStyleAutoDetect for a confidential client, got %v", conf.Endpoint.AuthStyle)
	}
}

// The redirect URI names the port actually bound, so an ephemeral port is a
// complete configuration rather than something the OAuth client had to agree to
// in advance.
func TestListenLoopback_RedirectNamesTheBoundPort(t *testing.T) {
	ln, redirect, err := listenLoopback(0)
	if err != nil {
		t.Fatalf("listenLoopback: %v", err)
	}
	defer ln.Close()
	if want := "http://" + ln.Addr().String(); redirect != want {
		t.Errorf("redirect = %q, want %q", redirect, want)
	}
	if strings.HasSuffix(redirect, ":0") {
		t.Errorf("redirect %q still names the placeholder port", redirect)
	}
}

// Two logins at once used to be a bind collision on one fixed port.
func TestListenLoopback_ConcurrentLoginsGetDistinctPorts(t *testing.T) {
	first, firstRedirect, err := listenLoopback(0)
	if err != nil {
		t.Fatalf("first listener: %v", err)
	}
	defer first.Close()
	second, secondRedirect, err := listenLoopback(0)
	if err != nil {
		t.Fatalf("second listener while the first holds a port: %v", err)
	}
	defer second.Close()
	if firstRedirect == secondRedirect {
		t.Errorf("both logins claimed %q", firstRedirect)
	}
}

// A pinned port is a promise the CLI cannot keep silently: it fails rather than
// falling back to one the IdP has not registered.
func TestListenLoopback_PinnedPortInUseFails(t *testing.T) {
	held, _, err := listenLoopback(0)
	if err != nil {
		t.Fatalf("listenLoopback: %v", err)
	}
	defer held.Close()
	port := held.Addr().(*net.TCPAddr).Port

	if _, _, err := listenLoopback(port); err == nil {
		t.Fatal("expected an error binding a port already held")
	} else if !strings.Contains(err.Error(), "login callback") {
		t.Errorf("error %q does not say what failed to bind", err)
	}
}

// Explicit endpoint env vars override the baked defaults without any network call.
func TestResolveEndpoints_EnvURLOverrides(t *testing.T) {
	t.Setenv("FPCLOUD_OIDC_AUTH_URL", "https://idp.example.com/authorize")
	t.Setenv("FPCLOUD_OIDC_TOKEN_URL", "https://idp.example.com/token")

	ep, err := resolveEndpoints(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ep.AuthURL != "https://idp.example.com/authorize" {
		t.Errorf("auth url not overridden: %q", ep.AuthURL)
	}
	if ep.TokenURL != "https://idp.example.com/token" {
		t.Errorf("token url not overridden: %q", ep.TokenURL)
	}
}

// With no overrides the endpoints default to Google.
func TestResolveEndpoints_DefaultsToGoogle(t *testing.T) {
	ep, err := resolveEndpoints(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ep.AuthURL != oidcAuthURL || ep.TokenURL != oidcTokenURL {
		t.Errorf("expected Google defaults, got %q / %q", ep.AuthURL, ep.TokenURL)
	}
}

// A client id is still mandatory (skipped if the binary was built with one embedded).
func TestResolveOIDCClient_RequiresID(t *testing.T) {
	if oidcClientID != "" {
		t.Skip("binary built with an embedded client id")
	}
	t.Setenv("FPCLOUD_OIDC_CLIENT_ID", "")
	t.Setenv("FPCLOUD_OIDC_CLIENT_SECRET", "")
	if _, _, err := resolveOIDCClient(); err == nil {
		t.Error("expected an error when no client id is set")
	}
}

// With no issuer/endpoint overrides, login is on the baked Google defaults, so
// the UX should say "Google".
func TestIdpName_GoogleByDefault(t *testing.T) {
	t.Setenv("FPCLOUD_OIDC_ISSUER", "")
	t.Setenv("FPCLOUD_OIDC_AUTH_URL", "")
	t.Setenv("FPCLOUD_OIDC_TOKEN_URL", "")
	if !usingGoogleDefaults() {
		t.Error("expected usingGoogleDefaults() true with no overrides")
	}
	if got := idpName(); got != "Google" {
		t.Errorf("idpName() = %q, want Google", got)
	}
}

// Pointing at a self-hosted issuer must not be reported as Google.
func TestIdpName_GenericWithIssuerOverride(t *testing.T) {
	t.Setenv("FPCLOUD_OIDC_ISSUER", "https://idp.example.com")
	if usingGoogleDefaults() {
		t.Error("an issuer override must not count as Google defaults")
	}
	if got := idpName(); got == "Google" {
		t.Errorf("idpName() should not be Google when an issuer is configured, got %q", got)
	}
}

// An explicit auth-endpoint override also de-Googles the messaging.
func TestIdpName_GenericWithEndpointOverride(t *testing.T) {
	t.Setenv("FPCLOUD_OIDC_ISSUER", "")
	t.Setenv("FPCLOUD_OIDC_AUTH_URL", "https://idp.example.com/authorize")
	if usingGoogleDefaults() {
		t.Error("an auth-url override must not count as Google defaults")
	}
}
