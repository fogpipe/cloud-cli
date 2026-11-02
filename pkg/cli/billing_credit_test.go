package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The credit lines beneath the cost total say, for the month in progress,
// what is left and when it runs out at the month's pace; a past month says
// only what was applied, since an unused part lapsed with it; and a month
// whose allowance is fully applied says so without a pace sentence.
func TestCreditLinesSayWhatIsLeftAndWhenItRunsOut(t *testing.T) {
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	october := november.AddDate(0, -1, 0)
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	runsOut := time.Date(2026, 11, 24, 6, 0, 0, 0, time.UTC)

	report := &client.CostReport{
		Currency: "EUR",
		Credits: []client.CostCredit{
			{PeriodStart: october, Allowance: "100.00", Applied: "88.30"},
			{PeriodStart: november, Allowance: "100.00", Applied: "63.40", RunsOutAt: &runsOut},
		},
	}
	assert.Equal(t, []string{
		"Credit 2026-10: 88.30 of 100.00 EUR applied",
		"Credit 2026-11: 63.40 of 100.00 EUR applied, 36.60 left, 13 days left in the month (UTC); at this month's pace it runs out around 24 Nov",
	}, creditLines(report, now))

	// Fully applied: what is above it is the amount due, said beneath.
	report.Credits = []client.CostCredit{{PeriodStart: november, Allowance: "100.00", Applied: "100.00"}}
	assert.Equal(t, []string{"Credit 2026-11: 100.00 of 100.00 EUR applied, fully used"}, creditLines(report, now))

	// Nothing the server could project: no pace sentence, never a blank one.
	report.Credits = []client.CostCredit{{PeriodStart: november, Allowance: "100.00", Applied: "0.00"}}
	assert.Equal(t, []string{"Credit 2026-11: 0.00 of 100.00 EUR applied, 100.00 left, 13 days left in the month (UTC)"},
		creditLines(report, time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)))

	lastDay := time.Date(2026, 11, 30, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, []string{"Credit 2026-11: 0.00 of 100.00 EUR applied, 100.00 left, last day of the month (UTC)"},
		creditLines(report, lastDay))
}

// Under a credit the invoice list says per month what the usage cost, what
// the credit took off and what is due; with no credited invoice it stays a
// total, since the extra columns would say nothing.
func TestInvoiceRowsSayUsageCreditAndDueOnlyUnderACredit(t *testing.T) {
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	september := october.AddDate(0, -1, 0)
	plain := []*client.Invoice{
		{ID: "inv-1", PeriodStart: september, Status: "finalized", Currency: "EUR", Total: "12.40"},
	}
	headers, rows := invoiceRows(plain)
	assert.Equal(t, []string{"PERIOD", "STATUS", "TOTAL", "ID"}, headers)
	assert.Equal(t, [][]string{{"2026-09", "finalized", "12.40 EUR", "inv-1"}}, rows)

	credited := []*client.Invoice{
		{ID: "inv-2", PeriodStart: october, Status: "finalized", Currency: "EUR", Total: "42.10", Credit: "100.00"},
		{ID: "inv-1", PeriodStart: september, Status: "finalized", Currency: "EUR", Total: "12.40"},
	}
	headers, rows = invoiceRows(credited)
	assert.Equal(t, []string{"PERIOD", "STATUS", "USAGE", "CREDIT", "DUE", "ID"}, headers)
	assert.Equal(t, [][]string{
		{"2026-10", "finalized", "142.10 EUR", "-100.00 EUR", "42.10 EUR", "inv-2"},
		{"2026-09", "finalized", "12.40 EUR", "-0.00 EUR", "12.40 EUR", "inv-1"},
	}, rows)
}

// `fpcloud billing credit` reads the org's credit route, and a month the
// org holds no credit for is a plain answer, not an error.
func TestBillingCreditReadsTheOrgsCreditAndSaysNonePlainly(t *testing.T) {
	const org = "6f1c1f24-3d0e-4a1f-9a3e-2b2b8f0d1a55"

	var requested []string
	held := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if !held {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"the org holds no credit"}`))
			return
		}
		_, _ = w.Write([]byte(`{"amount":"100.00","currency":"EUR","reason":"partner deal",` +
			`"effective_from":"2026-09-01T00:00:00Z","effective_to":"2027-04-01T00:00:00Z"}`))
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

	require.NoError(t, run("billing", "credit"))
	assert.Equal(t, []string{"/api/v1/orgs/" + org + "/billing/credit"}, requested)

	held = false
	require.NoError(t, run("billing", "credit"), "no credit is an answer, not a failure")
	assert.Error(t, run("billing", "credit", "extra"), "the command takes no argument")
}
