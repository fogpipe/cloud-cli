package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A template deploy answers with the app's own fields at the top level and
// what it made beside it under `created`, so a caller reads the database's and
// the bucket's names rather than deriving them from the app's.
func TestInstantiateTemplateReadsTheAppAndWhatItMade(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/projects/p1/templates/umami" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"a1","name":"stats","release":"3.4.0",
			"created":{"database":{"id":"d1","name":"stats-db"},"secrets":[{"id":"s1","name":"stats-app-secret"}]}}`))
	}))
	defer server.Close()

	dep, err := New(server.URL, "test-key").InstantiateTemplate(context.Background(), "p1", "umami", InstantiateTemplateRequest{Name: "stats"})
	if err != nil {
		t.Fatal(err)
	}
	if dep.ID != "a1" || dep.Name != "stats" || dep.Release != "3.4.0" {
		t.Errorf("app = %+v, want the top-level fields read as the app", dep.App)
	}
	if dep.Created.Database == nil || dep.Created.Database.Name != "stats-db" {
		t.Errorf("created database = %+v", dep.Created.Database)
	}
	if dep.Created.Bucket != nil {
		t.Errorf("created bucket = %+v, want none: the entry needed none", dep.Created.Bucket)
	}
	if len(dep.Created.Secrets) != 1 || dep.Created.Secrets[0].Name != "stats-app-secret" {
		t.Errorf("created secrets = %+v", dep.Created.Secrets)
	}
}
