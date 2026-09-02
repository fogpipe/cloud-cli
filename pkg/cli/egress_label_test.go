package cli

import (
	"strings"
	"testing"
)

// "all" is the widest mode we offer and it is not the whole internet: the
// platform refuses outbound 25 under every mode, so direct-to-MX delivery
// cannot earn its addresses a DNSBL listing. A tenant read "all (open)" as
// everything, and spent a day on an SMTP connection that hung (#236).
func TestEgressLabelAllNamesWhatItExcludes(t *testing.T) {
	label := egressLabel("all")
	if !strings.Contains(label, "25") {
		t.Errorf("egressLabel(all) = %q, want it to name tcp 25", label)
	}
}

// The tighter modes deny 25 themselves, so the platform's own deny is not the
// bound that binds there and naming it would describe the wrong one.
func TestEgressNoteOnlyOnAll(t *testing.T) {
	if egressNote("all") == "" {
		t.Error("egressNote(all) = \"\", want the outbound 25 exclusion")
	}
	for _, mode := range []string{"restricted", "https", ""} {
		if note := egressNote(mode); note != "" {
			t.Errorf("egressNote(%q) = %q, want empty", mode, note)
		}
	}
}
