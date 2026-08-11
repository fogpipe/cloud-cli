package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFKECredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/projects/demo/fke/credentials", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server":"https://k8s:6443","certificate_authority_data":"Zm9v","context":"fpcloud-fp-demo","namespace":"fp-demo"}`))
	}))
	defer server.Close()

	creds, err := New(server.URL, "k").FKECredentials(context.Background(), "demo")
	require.NoError(t, err)
	assert.Equal(t, "https://k8s:6443", creds.Server)
	assert.Equal(t, "Zm9v", creds.CertificateAuthorityData)
	assert.Equal(t, "fpcloud-fp-demo", creds.Context)
	assert.Equal(t, "fp-demo", creds.Namespace)
}

// TestFKECredentialsNotFound proves a 404 (an API too old to serve the endpoint)
// surfaces as ErrNotFound, so the CLI can fall back to embedded cluster constants.
func TestFKECredentialsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))
	defer server.Close()

	_, err := New(server.URL, "k").FKECredentials(context.Background(), "demo")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound), "404 must match ErrNotFound")
}

// TestAPIErrorIsOnlyMatches404 guards the sentinel: a non-404 error must not be
// mistaken for ErrNotFound (a 403 forbidden should never trigger the fallback).
func TestAPIErrorIsOnlyMatches404(t *testing.T) {
	assert.True(t, errors.Is(&APIError{StatusCode: http.StatusNotFound}, ErrNotFound))
	assert.False(t, errors.Is(&APIError{StatusCode: http.StatusForbidden}, ErrNotFound))
}

func TestClusterInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/cluster-info", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server":"https://k8s:6443","certificate_authority_data":"Zm9v"}`))
	}))
	defer server.Close()

	info, err := New(server.URL, "k").ClusterInfo(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "https://k8s:6443", info.Server)
	assert.Equal(t, "Zm9v", info.CertificateAuthorityData)
}

func TestFKEToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/projects/demo/fke/token", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"abc","expiration_timestamp":"2026-07-17T00:00:00Z"}`))
	}))
	defer server.Close()

	tok, err := New(server.URL, "k").FKEToken(context.Background(), "demo")
	require.NoError(t, err)
	assert.Equal(t, "abc", tok.Token)
	assert.Equal(t, "2026-07-17T00:00:00Z", tok.ExpirationTimestamp)
}
