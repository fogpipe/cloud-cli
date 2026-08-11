package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `auth logout` has to remove BOTH credentials, because there are two.
//
// It used to clear only `api_key` from config.yaml and print "Credentials
// removed". The cached Google refresh token stayed on disk, and getClient falls
// through to it — so the session kept working with full access after the user
// had been told their credentials were gone. On a shared or borrowed machine
// that is the difference between logging out and believing you have (#568).
func TestAuthLogout_RemovesBothCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FPCLOUD_STATE_DIR", dir)
	t.Setenv("FPCLOUD_CONFIG_DIR", dir)

	require.NoError(t, saveConfig(&Config{APIKey: "fp-key-abc"}))
	require.NoError(t, os.WriteFile(tokenCachePath(), []byte(`{"id_token":"x","refresh_token":"y"}`), 0o600))

	require.NoError(t, authLogoutCmd.RunE(authLogoutCmd, nil))

	cfg, err := loadConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.APIKey, "the api key must be cleared")

	_, err = os.Stat(tokenCachePath())
	assert.True(t, os.IsNotExist(err), "the cached browser session must be removed, got %v", err)
}

// Logging out twice is not an error, and neither is logging out having never
// logged in — the command reports what it cleared rather than claiming a removal
// that did not happen.
func TestAuthLogout_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FPCLOUD_STATE_DIR", dir)
	t.Setenv("FPCLOUD_CONFIG_DIR", dir)

	assert.NoError(t, authLogoutCmd.RunE(authLogoutCmd, nil))

	require.NoError(t, saveConfig(&Config{APIKey: "fp-key-abc"}))
	assert.NoError(t, authLogoutCmd.RunE(authLogoutCmd, nil))
	assert.NoError(t, authLogoutCmd.RunE(authLogoutCmd, nil))

	assert.NoFileExists(t, filepath.Join(dir, "oidc-token.json"))
}
