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
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// listenLoopback binds the OAuth callback listener and returns it with the
// redirect URI naming the port it actually got. port 0 takes an ephemeral one:
// the loopback redirect is built from the bound address rather than agreed in
// advance, so nothing has to be free before login starts and two logins can run
// side by side. The platform registers the CLI as a native client whose
// loopback redirect matches on path alone, which is what makes that legal;
// --port pins one for an IdP that registers an exact URI.
//
// 127.0.0.1 rather than localhost, on both sides: a listener on the name binds
// whichever family resolves first, and a browser that picks the other one gets
// connection refused with no way to tell why.
func listenLoopback(port int) (net.Listener, string, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, "", fmt.Errorf("cannot bind 127.0.0.1:%d for the login callback: %w", port, err)
	}
	return ln, "http://" + ln.Addr().String(), nil
}

// identityProvider is where this CLI signs people in: an issuer, the client the
// platform registered for the CLI, and the endpoints discovered from the issuer.
// This binary carries none of it (ADR-132 §5) — the control plane says where
// humans sign in through GET /api/v1/auth/config, the way it already says where
// the cluster is, so a from-source build logs in with nothing configured and a
// control plane someone else runs needs no environment on any machine.
//
// FPCLOUD_OIDC_ISSUER with FPCLOUD_OIDC_CLIENT_ID (and FPCLOUD_OIDC_CLIENT_SECRET
// for a confidential client) point at another issuer instead, for developing
// against one the control plane does not know yet.
type identityProvider struct {
	Issuer       string
	AuthURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	// OfflineAccess reports whether the issuer grants a refresh token through
	// the standard offline_access scope. One that does not (Google) is asked
	// its own way, with access_type=offline and a consent prompt.
	OfflineAccess bool
}

// resolveIdP finds the identity provider and discovers its endpoints.
func resolveIdP(ctx context.Context) (*identityProvider, error) {
	idp := &identityProvider{}
	if issuer := os.Getenv("FPCLOUD_OIDC_ISSUER"); issuer != "" {
		idp.Issuer = issuer
		idp.ClientID = os.Getenv("FPCLOUD_OIDC_CLIENT_ID")
		idp.ClientSecret = os.Getenv("FPCLOUD_OIDC_CLIENT_SECRET")
		if idp.ClientID == "" {
			return nil, fmt.Errorf("FPCLOUD_OIDC_ISSUER is set without FPCLOUD_OIDC_CLIENT_ID")
		}
	} else {
		apiURL := resolveAPIURL()
		cfg, err := newClient(apiURL, "").AuthConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("ask %s where to sign in: %w", apiURL, err)
		}
		idp.Issuer = cfg.Issuer
		idp.ClientID = cfg.CLIClientID
	}
	provider, err := oidc.NewProvider(ctx, idp.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover identity provider %s: %w", idp.Issuer, err)
	}
	ep := provider.Endpoint()
	idp.AuthURL = ep.AuthURL
	if idp.TokenURL == "" {
		idp.TokenURL = ep.TokenURL
	}
	var meta struct {
		ScopesSupported []string `json:"scopes_supported"`
	}
	if err := provider.Claims(&meta); err == nil {
		idp.OfflineAccess = slices.Contains(meta.ScopesSupported, oidc.ScopeOfflineAccess)
	}
	return idp, nil
}

// oauthConfig builds the OAuth client. redirectURL names the loopback callback
// this login is listening on; a refresh grant sends no redirect_uri and passes "".
func oauthConfig(idp *identityProvider, redirectURL string) *oauth2.Config {
	endpoint := oauth2.Endpoint{AuthURL: idp.AuthURL, TokenURL: idp.TokenURL}
	if idp.ClientSecret == "" {
		// Public client: send client_id in the request body and don't attempt HTTP
		// Basic auth with an empty secret. PKCE (S256) secures the code exchange.
		endpoint.AuthStyle = oauth2.AuthStyleInParams
	}
	scopes := []string{oidc.ScopeOpenID, "email", "profile"}
	if idp.OfflineAccess {
		scopes = append(scopes, oidc.ScopeOfflineAccess)
	}
	return &oauth2.Config{
		ClientID:     idp.ClientID,
		ClientSecret: idp.ClientSecret,
		Endpoint:     endpoint,
		RedirectURL:  redirectURL,
		Scopes:       scopes,
	}
}

// authOptions are the extra authorize parameters a login sends. Every login
// asks for the account picker: a browser that holds a session at the issuer
// would otherwise be signed straight in as whoever that session is, which is
// single sign-on working as designed and the wrong account whenever a person
// holds more than one. An issuer without offline_access is also asked for a
// refresh token its own way — Google issues one only under
// access_type=offline with a consent prompt.
//
// account names the login instead: sent as login_hint, the issuer uses that
// account's session without asking, or asks for that account and no other. The
// account is then in the command that produced the credential, which is what
// makes skipping the picker safe.
func authOptions(idp *identityProvider, account string) []oauth2.AuthCodeOption {
	var opts []oauth2.AuthCodeOption
	prompt := "select_account"
	if account != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", account))
		prompt = ""
	}
	if !idp.OfflineAccess {
		opts = append(opts, oauth2.AccessTypeOffline)
		prompt = strings.TrimSpace("consent " + prompt)
	}
	if prompt != "" {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", prompt))
	}
	return opts
}

// cachedToken records the issuer beside the tokens, so a refresh is attempted
// only against the issuer that minted them: a session from one identity
// provider is not carried into another.
type cachedToken struct {
	Issuer       string    `json:"issuer"`
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
		port, err := cmd.Flags().GetInt("port")
		if err != nil {
			return err
		}
		account, _ := cmd.Flags().GetString("account")
		return runLogin(context.Background(), port, account)
	},
}

func runLogin(ctx context.Context, port int, account string) error {
	ln, redirectURL, err := listenLoopback(port)
	if err != nil {
		return err
	}
	defer ln.Close()

	idp, err := resolveIdP(ctx)
	if err != nil {
		return err
	}
	conf := oauthConfig(idp, redirectURL)
	verifier := oauth2.GenerateVerifier()
	state, err := randomString()
	if err != nil {
		return err
	}

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

	authURL := conf.AuthCodeURL(state, append(authOptions(idp, account), oauth2.S256ChallengeOption(verifier))...)
	fmt.Println("Opening your browser to sign in…")
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
		return fmt.Errorf("no id_token returned by %s", idp.Issuer)
	}
	// A context belongs to the identity that chose it. Signing in as someone
	// else starts from no context at all, rather than inheriting an org and
	// project another person picked — which reads as theirs and fails as
	// theirs on every command (fogpipe/cloud-workspace#103).
	if previous, err := loadToken(); err == nil && emailFromIDToken(previous.IDToken) != emailFromIDToken(idToken) {
		if cfg, err := loadConfig(); err == nil && (cfg.CurrentOrg != "" || cfg.CurrentProject != "") {
			cfg.CurrentOrg, cfg.CurrentProject, cfg.CurrentOrgFKE = "", "", false
			if err := saveConfig(cfg); err != nil {
				return fmt.Errorf("clear the previous identity's context: %w", err)
			}
			fmt.Println(mutedStyle.Render("  Signed in as a different identity; the previous context was cleared."))
		}
	}
	if err := saveToken(&cachedToken{
		Issuer:       idp.Issuer,
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
	seedContext(ctx)
	return nil
}

// currentIDToken returns a valid OIDC ID token, transparently refreshing it
// from the cached refresh token when it's near expiry. It is the bearer
// credential for the control plane and, through the registry broker, the
// registry (see registry.go).
func currentIDToken() (string, error) {
	t, err := loadToken()
	if err != nil {
		return "", fmt.Errorf("not logged in — run `fpcloud login`: %w", err)
	}
	idToken := t.IDToken
	exp := idTokenExpiry(idToken)
	if idToken == "" || time.Now().After(exp.Add(-60*time.Second)) {
		ctx := context.Background()
		idp, err := resolveIdP(ctx)
		if err != nil {
			return "", err
		}
		if t.Issuer != idp.Issuer {
			return "", fmt.Errorf("your session is from %s, not from %s — run `fpcloud login`", firstNonEmpty(t.Issuer, "an earlier identity provider"), idp.Issuer)
		}
		// A refresh grant carries no redirect_uri — there is no callback to come back to.
		src := oauthConfig(idp, "").TokenSource(ctx, &oauth2.Token{
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
		// An issuer that rotates refresh tokens invalidates the old one on use,
		// so a save that fails is the next command failing to refresh.
		if err := saveToken(&cachedToken{
			Issuer:       t.Issuer,
			AccessToken:  newTok.AccessToken,
			RefreshToken: firstNonEmpty(newTok.RefreshToken, t.RefreshToken),
			IDToken:      idToken,
			Expiry:       newTok.Expiry,
		}); err != nil {
			return "", fmt.Errorf("save refreshed session: %w", err)
		}
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
// ID-token cluster-admin path).
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
	loginCmd.Flags().Int("port", 0, "Port for the local OAuth callback (0 picks a free one)")
	loginCmd.Flags().String("account", "", "Sign in as this login name without the account picker")
	rootCmd.AddCommand(loginCmd, getTokenCmd)
}
