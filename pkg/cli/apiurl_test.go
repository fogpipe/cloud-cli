package cli

import "testing"

// setAPIURLFlag mirrors what cobra does when --api-url is passed, restoring the
// value and its Changed bit afterwards — rootCmd is shared package state.
func setAPIURLFlag(t *testing.T, value string, changed bool) {
	t.Helper()
	flag := rootCmd.Flag("api-url")
	prev, prevChanged := flag.Value.String(), flag.Changed
	if err := flag.Value.Set(value); err != nil {
		t.Fatalf("setting --api-url: %v", err)
	}
	flag.Changed = changed
	t.Cleanup(func() {
		_ = flag.Value.Set(prev)
		flag.Changed = prevChanged
	})
}

// FPCLOUD_API_URL selects the control plane, the way it already does for the
// Terraform provider.
func TestResolveAPIURL_UsesEnv(t *testing.T) {
	t.Setenv("FPCLOUD_API_URL", "https://api.env.test")
	setAPIURLFlag(t, "https://api.default.test", false)

	if got := resolveAPIURL(); got != "https://api.env.test" {
		t.Errorf("expected the env URL, got %q", got)
	}
}

// An explicit --api-url wins over the environment.
func TestResolveAPIURL_FlagBeatsEnv(t *testing.T) {
	t.Setenv("FPCLOUD_API_URL", "https://api.env.test")
	setAPIURLFlag(t, "https://api.flag.test", true)

	if got := resolveAPIURL(); got != "https://api.flag.test" {
		t.Errorf("expected the flag URL, got %q", got)
	}
}

// Without the variable the flag's default stands — config.yaml's api_url, or
// the built-in default when config carries none.
func TestResolveAPIURL_FallsBackToFlagDefault(t *testing.T) {
	t.Setenv("FPCLOUD_API_URL", "")
	setAPIURLFlag(t, "https://api.config.test", false)

	if got := resolveAPIURL(); got != "https://api.config.test" {
		t.Errorf("expected the flag default, got %q", got)
	}
}

// getClient is the path every API-backed command takes; it must land on the
// same control plane the resolution promises.
func TestGetClient_UsesResolvedURL(t *testing.T) {
	t.Setenv("FPCLOUD_STATE_DIR", t.TempDir())
	t.Setenv("FPCLOUD_API_URL", "https://api.env.test")
	t.Setenv("FPCLOUD_API_KEY", "fp-env")
	setAPIURLFlag(t, "https://api.default.test", false)
	setAPIKeyFlag(t, "", false)

	c := getClient()
	if c.BaseURL != "https://api.env.test" {
		t.Errorf("client should call the resolved URL, got %q", c.BaseURL)
	}
	if c.APIKey != "fp-env" {
		t.Errorf("client should carry the resolved credential, got %q", c.APIKey)
	}
}
