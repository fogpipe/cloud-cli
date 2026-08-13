package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientStatesItsVersion(t *testing.T) {
	var stated string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stated = r.Header.Get(ClientVersionHeader)
		json.NewEncoder(w).Encode(Project{ID: "proj-1"})
	}))
	defer server.Close()

	c := New(server.URL, "test-key")
	c.Version = "v0.124.0"
	_, err := c.GetProject(context.Background(), "proj-1")

	require.NoError(t, err)
	assert.Equal(t, "v0.124.0", stated)
}

// An unknown version sends no header rather than an empty or invented one: a
// deployment refuses what it knows to be too old, and must not read "I cannot
// tell" as a version below its minimum.
func TestClientWithoutAVersionStatesNone(t *testing.T) {
	present := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, present = r.Header[ClientVersionHeader]
		json.NewEncoder(w).Encode(Project{ID: "proj-1"})
	}))
	defer server.Close()

	c := New(server.URL, "test-key")
	c.Version = ""
	_, err := c.GetProject(context.Background(), "proj-1")

	require.NoError(t, err)
	assert.False(t, present)
}

func TestClientTooOldIsMatchable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUpgradeRequired)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
			"code":    "CLIENT_TOO_OLD",
			"message": "fpcloud v0.120.0 is older than this deployment serves (minimum v0.124.0)",
		}})
	}))
	defer server.Close()

	_, err := New(server.URL, "test-key").GetProject(context.Background(), "proj-1")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrClientTooOld))
	assert.False(t, errors.Is(err, ErrNotFound))
	assert.Contains(t, err.Error(), "minimum v0.124.0")
}
