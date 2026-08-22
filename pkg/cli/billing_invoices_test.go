package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `fpcloud billing invoices <id>` used to accept the id, discard it and print
// the list — a successful-looking answer to a question nobody asked, and
// indistinguishable from the detail view while an org has only one invoice
// (fogpipe/cloud-workspace#102). Cobra allows a positional on any command that
// has a parent and subcommands, so nothing refused it and nothing read it.
//
// Both halves are pinned here: the id has to reach the per-invoice endpoint,
// and the bare form has to stay the list.
func TestBillingInvoicesRoutesOnTheNamedInvoice(t *testing.T) {
	const org = "6f1c1f24-3d0e-4a1f-9a3e-2b2b8f0d1a55"
	const invoice = "2a11900f-4773-47ce-8b6e-7182e628fdbf"

	var requested []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/orgs/"+org+"/billing/invoices" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"` + invoice + `","org_id":"` + org + `",` +
			`"period_start":"2026-07-01T00:00:00Z","period_end":"2026-08-01T00:00:00Z",` +
			`"status":"draft","currency":"EUR","total":"0","created_at":"2026-08-01T00:00:00Z",` +
			`"lines":[]}`))
	}))
	defer srv.Close()

	t.Setenv("FPCLOUD_API_URL", srv.URL)
	t.Setenv("FPCLOUD_API_KEY", "test-key")

	run := func(args ...string) error {
		rootCmd.SetArgs(append(args, "--org", org))
		defer func() {
			rootCmd.SetArgs(nil)
			_ = rootCmd.Flags().Set("org", "")
		}()
		return rootCmd.Execute()
	}

	require.NoError(t, run("billing", "invoices", invoice))
	assert.Equal(t, []string{"/api/v1/orgs/" + org + "/billing/invoices/" + invoice}, requested,
		"a named invoice must be fetched, not re-listed")

	requested = nil
	require.NoError(t, run("billing", "invoices"))
	assert.Equal(t, []string{"/api/v1/orgs/" + org + "/billing/invoices"}, requested)

	requested = nil
	assert.Error(t, run("billing", "invoices", invoice, "extra"),
		"a second positional names nothing, so it must be refused rather than ignored")
	assert.Empty(t, requested)
}
