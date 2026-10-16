package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// exchangeServer answers the exchange with a key naming the subject token it
// was given, and counts the exchanges, so a test can tell a cached credential
// from a fresh one.
func exchangeServer(t *testing.T, expiresIn int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var exchanges atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/token" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("the exchange carried a credential: %q", r.Header.Get("Authorization"))
		}
		var req TokenExchangeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		if req.GrantType != grantTokenExchange || req.SubjectTokenType != tokenTypeJWT {
			t.Errorf("request shape: %+v", req)
		}
		n := exchanges.Add(1)
		json.NewEncoder(w).Encode(TokenExchangeResponse{
			AccessToken: "fp-fed-" + req.SubjectToken + "-" + string(rune('0'+n)),
			TokenType:   "Bearer",
			ExpiresIn:   expiresIn,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &exchanges
}

func writeToken(t *testing.T, dir, token string) string {
	t.Helper()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPodIdentity_CachesUntilNearExpiry(t *testing.T) {
	srv, exchanges := exchangeServer(t, 900)
	path := writeToken(t, t.TempDir(), "k8s-a")
	now := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	p := NewPodIdentity(srv.URL, path)
	p.now = func() time.Time { return now }

	first, err := p.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first != "fp-fed-k8s-a-1" {
		t.Fatalf("first token %q", first)
	}
	// Well inside the lifetime: the held key is reused, the file is not read.
	now = now.Add(10 * time.Minute)
	writeToken(t, filepath.Dir(path), "k8s-b")
	if again, _ := p.Token(context.Background()); again != first {
		t.Fatalf("re-exchanged inside the lifetime: %q", again)
	}
	if exchanges.Load() != 1 {
		t.Fatalf("%d exchanges, want 1", exchanges.Load())
	}
	// Inside the refresh margin: a new exchange, reading the rotated file.
	now = now.Add(4*time.Minute + 30*time.Second)
	third, err := p.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if third != "fp-fed-k8s-b-2" {
		t.Fatalf("refreshed token %q", third)
	}
}

func TestPodIdentity_MissingFileIsAnError(t *testing.T) {
	srv, _ := exchangeServer(t, 900)
	p := NewPodIdentity(srv.URL, filepath.Join(t.TempDir(), "absent"))
	if _, err := p.Token(context.Background()); err == nil {
		t.Fatal("no error for a missing token file")
	}
}

// A client built for a pod sends the exchanged credential, and never the
// subject token, on an ordinary request.
func TestNewWithPodIdentity_RequestsCarryTheMintedKey(t *testing.T) {
	var seen string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token":
			json.NewEncoder(w).Encode(TokenExchangeResponse{AccessToken: "fp-fed-minted", TokenType: "Bearer", ExpiresIn: 900})
		case "/api/v1/version":
			seen = r.Header.Get("Authorization")
			w.Write([]byte(`{"version":"v0"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	path := writeToken(t, t.TempDir(), "k8s-subject")
	c := NewWithPodIdentity(api.URL, path)
	req, err := c.newRequest(context.Background(), http.MethodGet, "/api/v1/version", nil)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]string
	if err := c.do(req, &out); err != nil {
		t.Fatal(err)
	}
	if seen != "Bearer fp-fed-minted" {
		t.Fatalf("authorization %q", seen)
	}
}

func TestIdentityTokenFile(t *testing.T) {
	t.Setenv(IdentityTokenFileEnv, "")
	if got := IdentityTokenFile(); got != "" {
		t.Fatalf("nothing set: got %q", got)
	}
	t.Setenv(IdentityTokenFileEnv, "/var/run/secrets/fpcloud/token")
	if got := IdentityTokenFile(); got != "/var/run/secrets/fpcloud/token" {
		t.Fatalf("got %q", got)
	}
}
