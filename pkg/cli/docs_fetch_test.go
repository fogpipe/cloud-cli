package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDocsReadFromPlatform covers what stopped being a compile-time guarantee
// when `fpcloud docs` moved off the embedded pool: the CLI now names paths the
// server has to serve, and nothing but this test says it names them right.
func TestDocsReadFromPlatform(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/docs/index.json":
			_, _ = w.Write([]byte(`[{"topic":"quickstart","title":"Quickstart","summary":"Deploy in a minute","surfaces":["cli","llms"]},{"topic":"internals","title":"Internals","summary":"Not for the CLI","surfaces":["llms"]}]`))
		case "/docs/md/quickstart.md":
			_, _ = w.Write([]byte("# Quickstart\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	pool, err := fetchDocsIndex(srv.URL)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if len(pool) != 2 || pool[0].Topic != "quickstart" {
		t.Fatalf("index = %+v", pool)
	}

	body, err := fetchDocs(srv.URL, "/docs/md/quickstart.md")
	if err != nil {
		t.Fatalf("guide: %v", err)
	}
	if body != "# Quickstart\n" {
		t.Errorf("guide = %q", body)
	}

	if _, err := fetchDocs(srv.URL, "/docs/md/nope.md"); err == nil {
		t.Error("a topic the pool does not have should be an error")
	}
}

// TestDocsUnreachablePlatformNamesIt keeps the failure legible: without the
// host in the message, `fpcloud docs` against a wrong --api-url looks like the
// guide is missing rather than the platform being unreachable.
func TestDocsUnreachablePlatformNamesIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	_, err := fetchDocs(url, "/docs/index.json")
	if err == nil {
		t.Fatal("expected an error against a closed server")
	}
	if !strings.Contains(err.Error(), url) {
		t.Errorf("error %q does not name %s", err, url)
	}
}
