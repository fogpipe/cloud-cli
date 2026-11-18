package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// A growing type is read per GiB a month when it is billed by the hour, and
// per its own unit otherwise: logs are billed per GB written, with no month.
func TestAccrualRate(t *testing.T) {
	assert.Equal(t, "0.0365 EUR per GiB a month",
		accrualRate(client.QuoteAccrual{ResourceType: "database.backup_storage", Unit: "gib-hour", UnitPrice: "0.00005", MonthPrice: "0.0365"}, "EUR"))
	assert.Equal(t, "0.10 EUR per GB",
		accrualRate(client.QuoteAccrual{ResourceType: "compute.logs", Unit: "gb", UnitPrice: "0.10"}, "EUR"))
}

// A bucket's quote has nothing fixed, so it prints its storage rate and no
// total: a zero beside a rate reads as free.
func TestPrintQuote_NothingFixedPrintsTheRateNotATotal(t *testing.T) {
	require.NoError(t, rootCmd.Flag("output").Value.Set("table"))
	out := captureStdout(t, func() {
		require.NoError(t, printQuote(&client.Quote{
			Currency: "EUR", PriceBook: "list", Hours: 730, HourlyMin: "0", HourlyMax: "0", TotalMin: "0.00", TotalMax: "0.00",
			Accrues: []client.QuoteAccrual{{ResourceType: "bucket.storage", Unit: "gib-hour", UnitPrice: "0.00005", MonthPrice: "0.0365"}},
		}))
	})
	assert.Contains(t, out, "bucket.storage: 0.0365 EUR per GiB a month, as it fills")
	assert.NotContains(t, out, "0.00 EUR")
	assert.NotContains(t, out, "Estimate")
}
