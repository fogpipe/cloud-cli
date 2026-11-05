package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// With -o json or -o yaml, stdout is one document and nothing else. The summary
// a person reads under the table says what the document already carries, and
// printed after it, a strict parser refuses the answer and a lenient script reads
// no rows, which looks exactly like no usage.
func TestBillingReadsPrintOneDocument(t *testing.T) {
	const org = "6f1c1f24-3d0e-4a1f-9a3e-2b2b8f0d1a55"
	const invoice = "2a11900f-4773-47ce-8b6e-7182e628fdbf"
	report := `{"from":"2026-07-01T00:00:00Z","to":"2026-08-01T00:00:00Z","group_by":["resource_type"],` +
		`"currency":"EUR","price_books":[{"name":"list"}],"rows":[],"total":"1.20","unpriced":[]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/billing/invoices/"+invoice) {
			_, _ = w.Write([]byte(`{"id":"` + invoice + `","currency":"EUR","total":"1.20","status":"draft",` +
				`"period_start":"2026-07-01T00:00:00Z","period_end":"2026-08-01T00:00:00Z","lines":[]}`))
			return
		}
		_, _ = w.Write([]byte(report))
	}))
	defer srv.Close()
	t.Setenv("FPCLOUD_API_URL", srv.URL)
	t.Setenv("FPCLOUD_API_KEY", "test-key")

	reads := map[string][]string{
		"cost":          {"billing", "cost"},
		"invoice":       {"billing", "invoices", invoice},
		"invoice usage": {"billing", "invoices", invoice, "--by", "resource"},
	}
	formats := map[string]func(string) error{
		"json": func(out string) error {
			d := json.NewDecoder(strings.NewReader(out))
			var doc map[string]any
			if err := d.Decode(&doc); err != nil {
				return err
			}
			if err := d.Decode(&doc); !errors.Is(err, io.EOF) {
				return errors.New("something follows the document")
			}
			return nil
		},
		"yaml": func(out string) error {
			var doc map[string]any
			return yaml.Unmarshal([]byte(out), &doc)
		},
	}
	for name, args := range reads {
		for format, parse := range formats {
			t.Run(name+" "+format, func(t *testing.T) {
				rootCmd.SetArgs(append(append([]string{}, args...), "--org", org, "-o", format))
				defer func() {
					rootCmd.SetArgs(nil)
					_ = rootCmd.PersistentFlags().Set("output", "table")
					_ = rootCmd.Flags().Set("org", "")
					_ = billingInvoicesCmd.Flags().Set("by", "")
				}()
				var err error
				out := captureStdout(t, func() { err = rootCmd.Execute() })
				require.NoError(t, err)
				assert.NoError(t, parse(out), "stdout was:\n%s", out)
				assert.NotContains(t, out, "Total:", "the summary is for the table")
				assert.NotContains(t, out, "Usage:", "the summary is for the table")
			})
		}
	}
}
