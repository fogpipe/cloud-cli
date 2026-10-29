package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// A bare service account name is its scope's own account, resolved to the
// address the API takes; an address and a user pass through untouched, and a
// name the scope does not hold is refused naming the way out.
func TestResolveMemberNamesTheScopesOwnAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/projects/p1/service-accounts"):
			_ = json.NewEncoder(w).Encode([]client.ServiceAccount{{Name: "reader", Email: "reader@shop.k3f.cloud.fogpipe.com"}})
		case strings.HasSuffix(r.URL.Path, "/orgs/o1/service-accounts"):
			_ = json.NewEncoder(w).Encode([]client.ServiceAccount{{Name: "deployer", Email: "deployer@k3f.iam.cloud.fogpipe.com"}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := client.New(srv.URL, "k")
	project, org := &bindingTarget{}, &bindingTarget{Org: true}

	got, err := resolveMember(t.Context(), c, "serviceAccount", "reader", project, "", "p1")
	if err != nil || got != "reader@shop.k3f.cloud.fogpipe.com" {
		t.Fatalf("project name: got %q, %v", got, err)
	}
	got, err = resolveMember(t.Context(), c, "serviceAccount", "deployer", org, "o1", "")
	if err != nil || got != "deployer@k3f.iam.cloud.fogpipe.com" {
		t.Fatalf("organization name: got %q, %v", got, err)
	}
	for _, m := range [][2]string{{"serviceAccount", "ci@shop.k3f.cloud.fogpipe.com"}, {"user", "dev@example.com"}} {
		if got, err := resolveMember(t.Context(), c, m[0], m[1], project, "", "p1"); err != nil || got != m[1] {
			t.Errorf("%s:%s passes through, got %q, %v", m[0], m[1], got, err)
		}
	}
	if _, err := resolveMember(t.Context(), c, "serviceAccount", "nobody", project, "", "p1"); err == nil || !strings.Contains(err.Error(), `no service account named "nobody" in this project`) {
		t.Errorf("an unknown name is refused by name, got %v", err)
	}
}
