package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A service account created with -o json is printed as the account, one
// document, so a script that creates an identity reads its id and email back;
// the box and the hint to grant it a role are for a person.
func TestServiceAccountCreatePrintsOneDocument(t *testing.T) {
	const org = "6f1c1f24-3d0e-4a1f-9a3e-2b2b8f0d1a55"
	const project = "2a11900f-4773-47ce-8b6e-7182e628fdbf"
	account := `{"id":"e8b2ce9b-b3a0-451d-ba3b-0bbc06d66275","name":"ci","email":"ci@acme.cloud.fogpipe.com","status":"active"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/service-accounts") {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(account))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("FPCLOUD_API_URL", srv.URL)
	t.Setenv("FPCLOUD_API_KEY", "test-key")

	for name, args := range map[string][]string{
		"project":      {"sa", "create", "ci", "--project", project},
		"organization": {"sa", "create-org", "ci", "--org", org},
	} {
		t.Run(name, func(t *testing.T) {
			rootCmd.SetArgs(append(args, "-o", "json"))
			defer func() {
				rootCmd.SetArgs(nil)
				// Set marks a flag as given, which a later test reads as pinned,
				// so each one is put back as never given.
				for name, value := range map[string]string{"output": "table", "org": "", "project": ""} {
					f := rootCmd.PersistentFlags().Lookup(name)
					_ = f.Value.Set(value)
					f.Changed = false
				}
			}()
			var err error
			out := captureStdout(t, func() { err = rootCmd.Execute() })
			require.NoError(t, err)
			var got map[string]any
			require.NoError(t, json.Unmarshal([]byte(out), &got), "stdout was:\n%s", out)
			assert.Equal(t, "ci@acme.cloud.fogpipe.com", got["email"])
			assert.NotContains(t, out, "Grant it a role", "the hint is for the table")
		})
	}
}
