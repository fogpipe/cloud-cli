package cli

import "testing"

// setAPIKeyFlag mirrors what cobra does when --api-key is passed, and restores
// both the value and its Changed bit afterwards — rootCmd is package state
// shared by every test in this file.
func setAPIKeyFlag(t *testing.T, value string, changed bool) {
	t.Helper()
	flag := rootCmd.Flag("api-key")
	prev, prevChanged := flag.Value.String(), flag.Changed
	if err := flag.Value.Set(value); err != nil {
		t.Fatalf("setting --api-key: %v", err)
	}
	flag.Changed = changed
	t.Cleanup(func() {
		_ = flag.Value.Set(prev)
		flag.Changed = prevChanged
	})
}

// A credential in FPCLOUD_API_KEY authenticates every API call, not just the
// registry — a CI job that authenticated over OIDC can use the CLI.
func TestResolveAPIKey_UsesEnv(t *testing.T) {
	t.Setenv("FPCLOUD_STATE_DIR", t.TempDir())
	t.Setenv("FPCLOUD_API_KEY", "fp-env")
	setAPIKeyFlag(t, "", false)

	if got := resolveAPIKey(); got != "fp-env" {
		t.Errorf("expected the env credential, got %q", got)
	}
}

// An explicit --api-key wins over the environment.
func TestResolveAPIKey_FlagBeatsEnv(t *testing.T) {
	t.Setenv("FPCLOUD_STATE_DIR", t.TempDir())
	t.Setenv("FPCLOUD_API_KEY", "fp-env")
	setAPIKeyFlag(t, "fp-flag", true)

	if got := resolveAPIKey(); got != "fp-flag" {
		t.Errorf("expected the flag credential, got %q", got)
	}
}

// config.yaml supplies the flag's default, so a stored key must not shadow the
// environment the way a passed flag does.
func TestResolveAPIKey_EnvBeatsConfig(t *testing.T) {
	t.Setenv("FPCLOUD_STATE_DIR", t.TempDir())
	t.Setenv("FPCLOUD_API_KEY", "fp-env")
	setAPIKeyFlag(t, "fp-config", false)

	if got := resolveAPIKey(); got != "fp-env" {
		t.Errorf("expected the env credential to win over config.yaml, got %q", got)
	}
}

// With no env var the stored key still authenticates.
func TestResolveAPIKey_FallsBackToConfig(t *testing.T) {
	t.Setenv("FPCLOUD_STATE_DIR", t.TempDir())
	t.Setenv("FPCLOUD_API_KEY", "")
	setAPIKeyFlag(t, "fp-config", false)

	if got := resolveAPIKey(); got != "fp-config" {
		t.Errorf("expected the config credential, got %q", got)
	}
}

// Nothing anywhere resolves to no credential, which is what the 401 hint keys
// off — an unauthenticated request has to be distinguishable from a rejected one.
func TestResolveAPIKey_EmptyWhenNothingSet(t *testing.T) {
	t.Setenv("FPCLOUD_STATE_DIR", t.TempDir())
	t.Setenv("FPCLOUD_API_KEY", "")
	setAPIKeyFlag(t, "", false)

	if got := resolveAPIKey(); got != "" {
		t.Errorf("expected no credential, got %q", got)
	}
}
