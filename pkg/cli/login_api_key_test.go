package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `fpcloud login --api-key` is the one way to store a static key, and it stores
// nothing until the API has accepted it. Saving first meant a rejected key had
// already replaced a working one by the time it was checked (#568); the
// consolidation onto the root `login` keeps that order (fogpipe/cloud-workspace#360).
func TestLoginAPIKey_RejectedKeyLeavesTheStoredOneAlone(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FPCLOUD_STATE_DIR", dir)
	t.Setenv("FPCLOUD_CONFIG_DIR", dir)
	require.NoError(t, saveConfig(&Config{APIKey: "fp-working"}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer fp-good" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user":{"id":"u1","email":"a@example.com","name":"A"},"organization":{"id":"o1"}}`))
			return
		}
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	t.Setenv("FPCLOUD_API_URL", srv.URL)

	err := storeAPIKey(context.Background(), "fp-rejected")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rejected")
	cfg, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, "fp-working", cfg.APIKey, "a refused key changes nothing")

	require.NoError(t, storeAPIKey(context.Background(), "fp-good"))
	cfg, err = loadConfig()
	require.NoError(t, err)
	assert.Equal(t, "fp-good", cfg.APIKey)
}

// The auth subtree is gone, not aliased: `fpcloud auth ...` is an unknown
// command, and the verbs it held live at the root or under registry.
func TestAuthSubtreeIsDeleted(t *testing.T) {
	for _, name := range []string{"auth"} {
		cmd, _, err := rootCmd.Find([]string{name})
		assert.True(t, err != nil || cmd == rootCmd, "%q should not resolve to a command", name)
	}
	for _, path := range [][]string{{"login"}, {"logout"}, {"registry", "login"}, {"registry", "get-login-password"}, {"context"}} {
		cmd, _, err := rootCmd.Find(path)
		require.NoError(t, err)
		assert.Equal(t, path[len(path)-1], cmd.Name())
	}
}
