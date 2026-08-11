package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// The CLI's default OIDC client is a Google **Desktop-app** client (#392). Its client
// id is public and committed here; the client secret is never in this binary at all.
// Google requires it in the token exchange even with PKCE (confirmed: a secretless
// exchange is rejected with "client_secret is missing"), and a native app cannot keep
// one — so the platform holds it and the exchange is brokered through
// /api/v1/auth/oauth/token instead. That is what makes a plain `go build` of this repo
// a CLI that can actually log in, which is what nixpkgs and Homebrew core require.
// Setting FPCLOUD_OIDC_CLIENT_ID switches to a different client entirely — e.g. a
// self-hosted IdP (ADR-022), whose FPCLOUD_OIDC_CLIENT_SECRET may be empty (public/PKCE);
// that path talks to the issuer directly and never touches the broker.
var (
	oidcClientID     = "597394613214-a44p2lq2md2728dmfbtedolau3sn2d18.apps.googleusercontent.com"
	oidcClientSecret = "" // only ever set for a non-Google client, via FPCLOUD_OIDC_CLIENT_SECRET
	// OIDC endpoints default to Google; override per-issuer via ldflags or the
	// FPCLOUD_OIDC_* env vars. FPCLOUD_OIDC_ISSUER triggers .well-known discovery,
	// while FPCLOUD_OIDC_AUTH_URL / FPCLOUD_OIDC_TOKEN_URL set the endpoints directly.
	oidcIssuer   = ""
	oidcAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	oidcTokenURL = "https://oauth2.googleapis.com/token"
)

const (
	loopbackAddr = "localhost:61847"
	redirectURL  = "http://localhost:61847"
)

func resolveOIDCClient() (string, string, error) {
	// The committed Google Desktop client is the default (public, no secret —
	// PKCE-only). Setting FPCLOUD_OIDC_CLIENT_ID switches to a different client
	// entirely (e.g. a self-hosted IdP, ADR-022); its secret comes from
	// FPCLOUD_OIDC_CLIENT_SECRET (empty = public client, PKCE-only — no secret sent).
	id, secret := oidcClientID, oidcClientSecret
	if envID := os.Getenv("FPCLOUD_OIDC_CLIENT_ID"); envID != "" {
		id, secret = envID, os.Getenv("FPCLOUD_OIDC_CLIENT_SECRET")
	}
	if id == "" {
		return "", "", fmt.Errorf("fpcloud has no OIDC client id; set FPCLOUD_OIDC_CLIENT_ID (plus FPCLOUD_OIDC_CLIENT_SECRET for a confidential/Google client)")
	}
	return id, secret, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// resolveEndpoints determines the OAuth authorization/token endpoints. Precedence:
// explicit FPCLOUD_OIDC_AUTH_URL / FPCLOUD_OIDC_TOKEN_URL win; otherwise an issuer
// (FPCLOUD_OIDC_ISSUER or the ldflags-baked oidcIssuer) is resolved via OIDC
// discovery (<issuer>/.well-known/openid-configuration); otherwise the baked
// defaults (Google) are used.
func resolveEndpoints(ctx context.Context) (oauth2.Endpoint, error) {
	authURL := envOr("FPCLOUD_OIDC_AUTH_URL", oidcAuthURL)
	tokenURL := envOr("FPCLOUD_OIDC_TOKEN_URL", oidcTokenURL)

	if issuer := envOr("FPCLOUD_OIDC_ISSUER", oidcIssuer); issuer != "" {
		provider, err := oidc.NewProvider(ctx, issuer)
		if err != nil {
			return oauth2.Endpoint{}, fmt.Errorf("discover oidc issuer %q: %w", issuer, err)
		}
		ep := provider.Endpoint()
		// An explicit endpoint env var still overrides discovery.
		if os.Getenv("FPCLOUD_OIDC_AUTH_URL") != "" {
			ep.AuthURL = authURL
		}
		if os.Getenv("FPCLOUD_OIDC_TOKEN_URL") != "" {
			ep.TokenURL = tokenURL
		}
		return ep, nil
	}
	return oauth2.Endpoint{AuthURL: authURL, TokenURL: tokenURL}, nil
}

// usingGoogleDefaults reports whether login is pointed at the baked-in Google
// endpoints — i.e. no issuer and no endpoint override is configured, and the
// default auth URL is still Google's. It lets the UX say "Google" only when
// that's actually true, and stay generic for a self-hosted issuer (ADR-022).
func usingGoogleDefaults() bool {
	if envOr("FPCLOUD_OIDC_ISSUER", oidcIssuer) != "" {
		return false
	}
	if os.Getenv("FPCLOUD_OIDC_AUTH_URL") != "" || os.Getenv("FPCLOUD_OIDC_TOKEN_URL") != "" {
		return false
	}
	return strings.Contains(oidcAuthURL, "accounts.google.com")
}

// idpName is a human label for the configured identity provider, so login
// messages don't say "Google" when pointed at a self-hosted IdP.
func idpName() string {
	if usingGoogleDefaults() {
		return "Google"
	}
	return "your identity provider"
}

// brokerTokenPath is where the platform exchanges codes and refresh tokens on
// the CLI's behalf, holding the Google client secret this binary does not.
const brokerTokenPath = "/api/v1/auth/oauth/token"

func oauthConfig(ctx context.Context) (*oauth2.Config, error) {
	id, secret, err := resolveOIDCClient()
	if err != nil {
		return nil, err
	}
	endpoint, err := resolveEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	// Only the default Google client is brokered. A self-hosted issuer is either
	// public (PKCE, no secret to hold) or configured with its own secret here, and
	// in both cases talking to it directly is correct — routing it through our
	// platform would put a third party in the middle of someone else's login.
	if usingGoogleDefaults() && os.Getenv("FPCLOUD_OIDC_CLIENT_ID") == "" {
		endpoint.TokenURL = strings.TrimSuffix(rootCmd.Flag("api-url").Value.String(), "/") + brokerTokenPath
	}
	if secret == "" {
		// Public client: send client_id in the request body and don't attempt HTTP
		// Basic auth with an empty secret. PKCE (S256) secures the code exchange.
		endpoint.AuthStyle = oauth2.AuthStyleInParams
	}
	return &oauth2.Config{
		ClientID:     id,
		ClientSecret: secret,
		Endpoint:     endpoint,
		RedirectURL:  redirectURL,
		Scopes:       []string{"openid", "email", "profile"},
	}, nil
}

type cachedToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	IDToken      string    `json:"id_token"`
	Expiry       time.Time `json:"expiry"`
}

// The OIDC token is per-account, not per-project, so it lives in stateDir()
// (~/.fpcloud, or FPCLOUD_STATE_DIR) and is unaffected by FPCLOUD_CONFIG_DIR — a
// per-project config dir reuses the same login.
func tokenCachePath() string { return filepath.Join(stateDir(), "oidc-token.json") }

func saveToken(t *cachedToken) error {
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(tokenCachePath(), data, 0o600)
}

func loadToken() (*cachedToken, error) {
	data, err := os.ReadFile(tokenCachePath())
	if err != nil {
		return nil, err
	}
	t := &cachedToken{}
	if err := json.Unmarshal(data, t); err != nil {
		return nil, err
	}
	return t, nil
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in via OIDC (authentication only; kubectl access is `fpcloud fke get-credentials`)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		conf, err := oauthConfig(ctx)
		if err != nil {
			return err
		}
		verifier := oauth2.GenerateVerifier()
		state, err := randomString()
		if err != nil {
			return err
		}

		ln, err := net.Listen("tcp", loopbackAddr)
		if err != nil {
			return fmt.Errorf("cannot bind %s for the login callback: %w", loopbackAddr, err)
		}
		defer ln.Close()

		codeCh := make(chan string, 1)
		errCh := make(chan error, 1)
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if e := q.Get("error"); e != "" {
				fmt.Fprint(w, "Login failed. You can close this tab.")
				errCh <- fmt.Errorf("authorization failed: %s", e)
				return
			}
			if q.Get("state") != state {
				fmt.Fprint(w, "Login failed (state mismatch). You can close this tab.")
				errCh <- fmt.Errorf("state mismatch in callback")
				return
			}
			fmt.Fprint(w, "Login successful — you can close this tab and return to your terminal.")
			codeCh <- q.Get("code")
		})}
		go srv.Serve(ln)
		defer srv.Close()

		authURL := conf.AuthCodeURL(state,
			oauth2.AccessTypeOffline,
			oauth2.S256ChallengeOption(verifier),
			oauth2.SetAuthURLParam("prompt", "consent"),
		)
		fmt.Printf("Opening your browser to sign in with %s…\n", idpName())
		fmt.Println(mutedStyle.Render("  If it doesn't open, visit:\n  " + authURL))
		_ = openBrowser(authURL)

		var code string
		select {
		case code = <-codeCh:
		case err = <-errCh:
			return err
		case <-time.After(3 * time.Minute):
			return fmt.Errorf("timed out waiting for browser login")
		}

		tok, err := conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
		if err != nil {
			return fmt.Errorf("token exchange failed: %w", err)
		}
		idToken, _ := tok.Extra("id_token").(string)
		if idToken == "" {
			return fmt.Errorf("no id_token returned by %s", idpName())
		}
		if err := saveToken(&cachedToken{
			AccessToken:  tok.AccessToken,
			RefreshToken: tok.RefreshToken,
			IDToken:      idToken,
			Expiry:       tok.Expiry,
		}); err != nil {
			return err
		}
		fmt.Println()
		fmt.Println(successBox.Render(fmt.Sprintf("✓ Logged in as %s", emailFromIDToken(idToken))))
		fmt.Println(mutedStyle.Render("  For kubectl access, run:  fpcloud fke get-credentials [--project <name>]"))
		return nil
	},
}

// currentIDToken returns a valid Google OIDC ID token, transparently
// refreshing it from the cached refresh token when it's near expiry. The
// cluster trusts Google OIDC, so this token doubles as the bearer credential
// for direct k8s API calls (see registry.go).
func currentIDToken() (string, error) {
	t, err := loadToken()
	if err != nil {
		return "", fmt.Errorf("not logged in — run `fpcloud login`: %w", err)
	}
	idToken := t.IDToken
	exp := idTokenExpiry(idToken)
	if idToken == "" || time.Now().After(exp.Add(-60*time.Second)) {
		ctx := context.Background()
		conf, err := oauthConfig(ctx)
		if err != nil {
			return "", err
		}
		src := conf.TokenSource(ctx, &oauth2.Token{
			RefreshToken: t.RefreshToken,
			Expiry:       time.Now().Add(-time.Hour),
		})
		newTok, err := src.Token()
		if err != nil {
			return "", fmt.Errorf("token refresh failed — run `fpcloud login`: %w", err)
		}
		if nid, _ := newTok.Extra("id_token").(string); nid != "" {
			idToken = nid
		}
		_ = saveToken(&cachedToken{
			AccessToken:  newTok.AccessToken,
			RefreshToken: firstNonEmpty(newTok.RefreshToken, t.RefreshToken),
			IDToken:      idToken,
			Expiry:       newTok.Expiry,
		})
	}
	return idToken, nil
}

var getTokenCmd = &cobra.Command{
	Use:    "get-token",
	Short:  "Print a kubectl ExecCredential (used by the kubeconfig; not for direct use)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		idToken, err := currentIDToken()
		if err != nil {
			return err
		}
		out := map[string]any{
			"apiVersion": "client.authentication.k8s.io/v1",
			"kind":       "ExecCredential",
			"status": map[string]any{
				"token":               idToken,
				"expirationTimestamp": idTokenExpiry(idToken).UTC().Format(time.RFC3339),
			},
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	},
}

// kubeconfigEntry is the cluster/user/context triple written into a kubeconfig by
// writeKubeconfig. server + caData come from the API (fke get-credentials) rather
// than embedded constants, so the binary is cluster-agnostic; execArgs is the
// exec-plugin argv that mints the bearer token (e.g. {"fke","get-token",
// "--project","myproj"} for a scoped tenant token, or {"get-token"} for the operator
// Google-ID-token cluster-admin path).
type kubeconfigEntry struct {
	context   string
	server    string
	caData    []byte
	namespace string
	execArgs  []string
}

// writeKubeconfig merges the given cluster/user/context into the kubeconfig at
// path (or the default $KUBECONFIG / ~/.kube/config), sets it current, and
// returns the file written. Merge semantics match gcloud/aws (clientcmd.ModifyConfig).
func writeKubeconfig(path string, e kubeconfigEntry) (string, error) {
	po := clientcmd.NewDefaultPathOptions() // honors $KUBECONFIG, else ~/.kube/config
	if path != "" {
		po.LoadingRules.ExplicitPath = path
		po.LoadingRules.Precedence = nil
		po.EnvVar = ""
	}
	cfg, err := po.GetStartingConfig()
	if err != nil {
		return "", err
	}
	cfg.Clusters[e.context] = &clientcmdapi.Cluster{
		Server:                   e.server,
		CertificateAuthorityData: e.caData,
	}
	cfg.AuthInfos[e.context] = &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			APIVersion:      "client.authentication.k8s.io/v1",
			Command:         "fpcloud",
			Args:            e.execArgs,
			InteractiveMode: clientcmdapi.IfAvailableExecInteractiveMode,
		},
	}
	cfg.Contexts[e.context] = &clientcmdapi.Context{Cluster: e.context, AuthInfo: e.context, Namespace: e.namespace}
	cfg.CurrentContext = e.context
	if err := clientcmd.ModifyConfig(po, *cfg, true); err != nil {
		return "", err
	}
	return po.GetDefaultFilename(), nil
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func randomString() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func idTokenExpiry(idToken string) time.Time {
	if exp, ok := parseJWTClaims(idToken)["exp"].(float64); ok {
		return time.Unix(int64(exp), 0)
	}
	return time.Now().Add(55 * time.Minute)
}

func emailFromIDToken(idToken string) string {
	if e, ok := parseJWTClaims(idToken)["email"].(string); ok {
		return e
	}
	return "you"
}

func parseJWTClaims(jwt string) map[string]any {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	m := map[string]any{}
	_ = json.Unmarshal(payload, &m)
	return m
}

func init() {
	rootCmd.AddCommand(loginCmd, getTokenCmd)
}
