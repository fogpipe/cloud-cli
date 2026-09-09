package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// The one property that matters: exactly one state may render as clean. Every
// other state — including one this binary does not recognise — has to say that
// an empty table below it was never a measurement (#902, #934).
func TestScanStateLineOnlyScannedReadsClean(t *testing.T) {
	notClean := []string{
		client.ScanStateRefused,
		client.ScanStateFailed,
		client.ScanStatePending,
		client.ScanStateExternal,
		client.ScanStateUnresolved,
		"a-state-from-a-newer-server",
	}
	for _, state := range notClean {
		t.Run(state, func(t *testing.T) {
			got := scanStateLine(state, "", nil, 0)
			if strings.Contains(got, "no vulnerabilities found") {
				t.Fatalf("state %q rendered as clean: %q", state, got)
			}
			if !strings.Contains(got, "Nothing below") && !strings.Contains(got, "nothing below") {
				t.Fatalf("state %q does not disclaim the empty table: %q", state, got)
			}
		})
	}
}

// The positive control: without it, a function that returned the disclaimer for
// every input would pass the test above.
func TestScanStateLineScannedReadsClean(t *testing.T) {
	got := scanStateLine(client.ScanStateScanned, "", nil, 0)
	if !strings.Contains(got, "no vulnerabilities found") {
		t.Fatalf("a scanned image with no findings must read as clean, got %q", got)
	}
	if strings.Contains(got, "Nothing below") {
		t.Fatalf("a scanned image must not disclaim its own result, got %q", got)
	}
}

func TestScanStateLineScannedNamesWhenAndCount(t *testing.T) {
	at := time.Date(2026, 9, 9, 14, 30, 0, 0, time.UTC)
	got := scanStateLine(client.ScanStateScanned, "", &at, 3)
	if !strings.Contains(got, "3 vulnerabilities") {
		t.Fatalf("findings count missing: %q", got)
	}
	// A count with no date is a claim about an image as of an unstated moment.
	if !strings.Contains(got, "2026-09-09") {
		t.Fatalf("scan time missing: %q", got)
	}
}

func TestScanStateLineCarriesReason(t *testing.T) {
	got := scanStateLine(client.ScanStateRefused, "image exceeds the scratch ceiling", nil, 0)
	if !strings.Contains(got, "image exceeds the scratch ceiling") {
		t.Fatalf("reason dropped: %q", got)
	}
}
