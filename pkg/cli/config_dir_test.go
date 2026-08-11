package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigDir_HonorsFPCLOUDCONFIGDIR(t *testing.T) {
	t.Setenv("FPCLOUD_CONFIG_DIR", "/tmp/proj/.fpcloud")
	if got := configDir(); got != "/tmp/proj/.fpcloud" {
		t.Errorf("expected FPCLOUD_CONFIG_DIR path, got %q", got)
	}
}

func TestConfigDir_DefaultsToHome(t *testing.T) {
	t.Setenv("FPCLOUD_CONFIG_DIR", "")
	t.Setenv("FPCLOUD_STATE_DIR", "")
	got := configDir()
	if !strings.HasSuffix(got, ".fpcloud") {
		t.Errorf("expected default ~/.fpcloud, got %q", got)
	}
}

// A per-project config dir (FPCLOUD_CONFIG_DIR) scopes only the config file:
// saveConfig creates the dir and writes config.yaml there, and loadConfig reads
// it back. Per-account state (OIDC token, version stamp) stays in stateDir().
func TestSaveLoadConfig_PerProjectDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub")
	t.Setenv("FPCLOUD_CONFIG_DIR", dir)

	if err := saveConfig(&Config{CurrentOrg: "acme", CurrentProject: "demo"}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatalf("config.yaml not written to FPCLOUD_CONFIG_DIR: %v", err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.CurrentOrg != "acme" || cfg.CurrentProject != "demo" {
		t.Errorf("round-trip mismatch: %+v", cfg)
	}
}

// FPCLOUD_CONFIG_DIR scopes org/project context per directory, but the OIDC token
// is per-account: it must stay in the global stateDir() (~/.fpcloud) so entering a
// per-project dir reuses the existing login instead of forcing a re-auth.
func TestTokenAndVersionPathsIgnoreConfigDir(t *testing.T) {
	t.Setenv("FPCLOUD_STATE_DIR", "")
	t.Setenv("FPCLOUD_CONFIG_DIR", "/tmp/proj/.fpcloud")
	if got := tokenCachePath(); !strings.HasSuffix(got, "/.fpcloud/oidc-token.json") || strings.HasPrefix(got, "/tmp/proj") {
		t.Errorf("token path should stay in global ~/.fpcloud, got %q", got)
	}
	if got := versionCachePath(); !strings.HasSuffix(got, "/.fpcloud/version-check.json") || strings.HasPrefix(got, "/tmp/proj") {
		t.Errorf("version path should stay in global ~/.fpcloud, got %q", got)
	}
}

// FPCLOUD_STATE_DIR relocates the credential/state store so a repo can keep every
// byte of fpcloud state — login token included — inside the project directory.
func TestStateDir_HonorsFPCLOUDSTATEDIR(t *testing.T) {
	t.Setenv("FPCLOUD_STATE_DIR", "/tmp/proj/.fpcloud")
	if got := stateDir(); got != "/tmp/proj/.fpcloud" {
		t.Errorf("expected FPCLOUD_STATE_DIR path, got %q", got)
	}
	if got := tokenCachePath(); got != "/tmp/proj/.fpcloud/oidc-token.json" {
		t.Errorf("token path should follow FPCLOUD_STATE_DIR, got %q", got)
	}
	if got := versionCachePath(); got != "/tmp/proj/.fpcloud/version-check.json" {
		t.Errorf("version path should follow FPCLOUD_STATE_DIR, got %q", got)
	}
	if got := storageCredsDir(); got != "/tmp/proj/.fpcloud/storage" {
		t.Errorf("storage creds should follow FPCLOUD_STATE_DIR, got %q", got)
	}
}

// With only FPCLOUD_STATE_DIR set, config.yaml follows it too — one variable
// relocates everything (gcloud's CLOUDSDK_CONFIG). FPCLOUD_CONFIG_DIR still wins
// for the config file alone.
func TestConfigDir_FollowsStateDir(t *testing.T) {
	t.Setenv("FPCLOUD_CONFIG_DIR", "")
	t.Setenv("FPCLOUD_STATE_DIR", "/tmp/proj/.fpcloud")
	if got := configDir(); got != "/tmp/proj/.fpcloud" {
		t.Errorf("configDir should follow FPCLOUD_STATE_DIR, got %q", got)
	}
	t.Setenv("FPCLOUD_CONFIG_DIR", "/tmp/other")
	if got := configDir(); got != "/tmp/other" {
		t.Errorf("FPCLOUD_CONFIG_DIR should win for config.yaml, got %q", got)
	}
	if got := tokenCachePath(); got != "/tmp/proj/.fpcloud/oidc-token.json" {
		t.Errorf("token path should stay in FPCLOUD_STATE_DIR, got %q", got)
	}
}
