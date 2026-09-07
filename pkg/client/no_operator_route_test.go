package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Operating the platform is its own binary (ADR-089): the tenant SDK carries no
// operator method. Four survived under /admin with no caller left — the
// provider's org ceiling and FKE attributes they were kept for had already
// gone (fogpipe/cloud-workspace#121) — and nothing said so, because a method
// nobody calls compiles. Held here, over the package's own source.
func TestClientReachesNoOperatorRoute(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, prefix := range []string{"/api/v1/admin/", "/api/v1/operator/"} {
			if strings.Contains(string(body), prefix) {
				t.Errorf("%s reaches %s — an operator route belongs to fpadmin (ADR-089)", f, prefix)
			}
		}
	}
}
