package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// fakeIssuer serves OIDC discovery for itself and a token endpoint that answers
// every grant with the tokens it is given.
func fakeIssuer(t *testing.T, scopes []string, token map[string]any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 srv.URL,
			"authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint":         srv.URL + "/token",
			"jwks_uri":               srv.URL + "/keys",
			"scopes_supported":       scopes,
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(token)
	})
	return srv
}

// fakeControlPlane answers /api/v1/auth/config.
func fakeControlPlane(t *testing.T, cfg map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/config" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(cfg)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func unsignedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + "."
}

// With nothing in the environment the CLI asks the control plane where to sign
// in, and takes the client it registered.
func TestResolveIdP_AsksTheControlPlane(t *testing.T) {
	issuer := fakeIssuer(t, []string{"openid", "email", "profile", "offline_access"}, nil)
	api := fakeControlPlane(t, map[string]any{"issuer": issuer.URL, "cli_client_id": "cli-client"})
	t.Setenv("FPCLOUD_OIDC_ISSUER", "")
	t.Setenv("FPCLOUD_API_URL", api.URL)

	idp, err := resolveIdP(context.Background())
	if err != nil {
		t.Fatalf("resolveIdP: %v", err)
	}
	if idp.Issuer != issuer.URL || idp.ClientID != "cli-client" || idp.ClientSecret != "" {
		t.Errorf("idp = %+v, want the control plane's issuer and client", idp)
	}
	if idp.AuthURL != issuer.URL+"/authorize" || idp.TokenURL != issuer.URL+"/token" {
		t.Errorf("endpoints %q / %q were not discovered from the issuer", idp.AuthURL, idp.TokenURL)
	}
	if !idp.OfflineAccess {
		t.Error("an issuer listing offline_access should be asked for it")
	}
}

// An issuer that demands a secret from a native client is brokered: the code
// goes to the platform, which holds the secret, while the browser still goes to
// the issuer (ADR-068).
func TestResolveIdP_BrokeredExchangeGoesToThePlatform(t *testing.T) {
	issuer := fakeIssuer(t, []string{"openid", "email", "profile"}, nil)
	api := fakeControlPlane(t, map[string]any{"issuer": issuer.URL, "cli_client_id": "cli-client", "broker_path": "/api/v1/auth/oauth/token"})
	t.Setenv("FPCLOUD_OIDC_ISSUER", "")
	t.Setenv("FPCLOUD_API_URL", api.URL)

	idp, err := resolveIdP(context.Background())
	if err != nil {
		t.Fatalf("resolveIdP: %v", err)
	}
	if idp.TokenURL != api.URL+"/api/v1/auth/oauth/token" {
		t.Errorf("token url = %q, want the broker", idp.TokenURL)
	}
	if idp.AuthURL != issuer.URL+"/authorize" {
		t.Errorf("auth url = %q, want the issuer's", idp.AuthURL)
	}
	if idp.OfflineAccess {
		t.Error("an issuer not listing offline_access must be asked for a refresh token its own way")
	}
	opts := refreshOptions(idp)
	if len(opts) == 0 {
		t.Error("expected access_type=offline and a consent prompt for an issuer without offline_access")
	}
}

// The environment overrides the control plane for developing against another
// issuer, and needs both halves: an issuer without a client is a mistake, not a
// login against nothing.
func TestResolveIdP_EnvOverride(t *testing.T) {
	issuer := fakeIssuer(t, []string{"openid", "offline_access"}, nil)
	t.Setenv("FPCLOUD_OIDC_ISSUER", issuer.URL)
	t.Setenv("FPCLOUD_OIDC_CLIENT_ID", "")
	t.Setenv("FPCLOUD_API_URL", "http://127.0.0.1:1")

	if _, err := resolveIdP(context.Background()); err == nil || !strings.Contains(err.Error(), "FPCLOUD_OIDC_CLIENT_ID") {
		t.Fatalf("expected an error naming the missing client id, got %v", err)
	}

	t.Setenv("FPCLOUD_OIDC_CLIENT_ID", "dev-client")
	t.Setenv("FPCLOUD_OIDC_CLIENT_SECRET", "shh")
	idp, err := resolveIdP(context.Background())
	if err != nil {
		t.Fatalf("resolveIdP: %v", err)
	}
	if idp.ClientID != "dev-client" || idp.ClientSecret != "shh" || idp.TokenURL != issuer.URL+"/token" {
		t.Errorf("idp = %+v, want the environment's client against the issuer's own endpoints", idp)
	}
}

// A public client sends client_id in the body and asks for offline_access; a
// confidential one keeps its secret and the auto-detected auth style.
func TestOAuthConfig_PublicAndConfidentialClients(t *testing.T) {
	public := oauthConfig(&identityProvider{ClientID: "public", OfflineAccess: true}, "http://127.0.0.1:1234")
	if public.Endpoint.AuthStyle != oauth2.AuthStyleInParams {
		t.Errorf("expected AuthStyleInParams for a public client, got %v", public.Endpoint.AuthStyle)
	}
	if strings.Join(public.Scopes, " ") != "openid email profile offline_access" {
		t.Errorf("scopes = %v", public.Scopes)
	}
	confidential := oauthConfig(&identityProvider{ClientID: "conf", ClientSecret: "shh"}, "")
	if confidential.ClientSecret != "shh" || confidential.Endpoint.AuthStyle != oauth2.AuthStyleAutoDetect {
		t.Errorf("confidential client lost its secret or auth style: %+v", confidential)
	}
	if strings.Join(confidential.Scopes, " ") != "openid email profile" {
		t.Errorf("scopes = %v, offline_access must not be asked of an issuer that does not list it", confidential.Scopes)
	}
}

// A session minted by one issuer is not refreshed against another: the cache
// names its issuer, and a mismatch is a new login, not a confusing 401.
func TestCurrentIDToken_RefusesAnotherIssuersSession(t *testing.T) {
	issuer := fakeIssuer(t, nil, nil)
	t.Setenv("FPCLOUD_STATE_DIR", t.TempDir())
	t.Setenv("FPCLOUD_OIDC_ISSUER", issuer.URL)
	t.Setenv("FPCLOUD_OIDC_CLIENT_ID", "cli")
	expired := unsignedJWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})
	if err := saveToken(&cachedToken{Issuer: "https://accounts.google.com", IDToken: expired, RefreshToken: "old"}); err != nil {
		t.Fatal(err)
	}

	_, err := currentIDToken()
	if err == nil || !strings.Contains(err.Error(), "fpcloud login") {
		t.Fatalf("expected a refusal pointing at fpcloud login, got %v", err)
	}
}

// A rotated refresh token is persisted, because the issuer has already
// invalidated the one it replaced.
func TestCurrentIDToken_PersistsRotatedRefreshToken(t *testing.T) {
	fresh := unsignedJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "email": "a@example.com"})
	issuer := fakeIssuer(t, nil, map[string]any{
		"access_token": "at2", "refresh_token": "rotated", "id_token": fresh, "token_type": "Bearer", "expires_in": 3600,
	})
	t.Setenv("FPCLOUD_STATE_DIR", t.TempDir())
	t.Setenv("FPCLOUD_OIDC_ISSUER", issuer.URL)
	t.Setenv("FPCLOUD_OIDC_CLIENT_ID", "cli")
	expired := unsignedJWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})
	if err := saveToken(&cachedToken{Issuer: issuer.URL, IDToken: expired, RefreshToken: "old"}); err != nil {
		t.Fatal(err)
	}

	got, err := currentIDToken()
	if err != nil {
		t.Fatalf("currentIDToken: %v", err)
	}
	if got != fresh {
		t.Errorf("returned the stale id token")
	}
	saved, err := loadToken()
	if err != nil {
		t.Fatal(err)
	}
	if saved.RefreshToken != "rotated" || saved.Issuer != issuer.URL {
		t.Errorf("saved = %+v, want the rotated refresh token under the same issuer", saved)
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
