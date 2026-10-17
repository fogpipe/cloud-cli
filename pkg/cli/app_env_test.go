package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// --reveal reads each referenced secret once, puts its value where the
// reference was, leaves a literal as it is, and reports a refused read as
// refused rather than as a secret holding nothing (ADR-289).
func TestRevealConfigs_ReadsEachSecretOnceAndNamesARefusal(t *testing.T) {
	var reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/p1/secrets":
			_ = json.NewEncoder(w).Encode([]client.Secret{{ID: "s1", Name: "stripe-key", Scope: "project"}, {ID: "s2", Name: "db-owner", Scope: "project"}})
		case "/api/v1/secrets/s1/value":
			reads.Add(1)
			_ = json.NewEncoder(w).Encode(client.SecretValue{Value: "sk_test_123"})
		case "/api/v1/secrets/s2/value":
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	prev := rootCmd.Flag("project").Value.String()
	require.NoError(t, rootCmd.Flag("project").Value.Set("p1"))
	defer func() { _ = rootCmd.Flag("project").Value.Set(prev) }()

	configs := []*client.AppConfig{
		{Key: "LOG_LEVEL", Value: "info"},
		{Key: "STRIPE_KEY", SecretRef: &client.SecretRef{Secret: "stripe-key"}},
		{Key: "STRIPE_KEY_FILE", SecretRef: &client.SecretRef{Secret: "stripe-key", Path: "/secrets/stripe"}},
		{Key: "DATABASE_URL", SecretRef: &client.SecretRef{Secret: "db-owner"}},
	}
	got := revealConfigs(t.Context(), client.New(srv.URL, "k"), configs)

	require.Equal(t, "info", got[0].Value)
	require.Empty(t, got[0].RevealError)
	require.Equal(t, "sk_test_123", got[1].Value)
	require.Equal(t, "sk_test_123", got[2].Value)
	require.Empty(t, got[1].RevealError)
	require.Empty(t, got[3].Value)
	require.NotEmpty(t, got[3].RevealError)
	require.Equal(t, int32(1), reads.Load(), "a secret two variables reference is read once")
}
