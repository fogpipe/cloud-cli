package cli

import (
	"strings"
	"testing"
)

// "all" is the widest mode we offer and it is not the whole internet: the host
// network drops outbound 25 and 465 under every mode. A tenant read "all (open)"
// as everything, and spent a day on an SMTP connection that hung (#236).
func TestEgressLabelAllNamesWhatItExcludes(t *testing.T) {
	label := egressLabel("all")
	for _, port := range []string{"25", "465"} {
		if !strings.Contains(label, port) {
			t.Errorf("egressLabel(all) = %q, want it to name tcp %s", label, port)
		}
	}
}

// The tighter modes deny both ports themselves, so the host block is not the
// bound that binds there and naming it would describe the wrong one.
func TestEgressNoteOnlyOnAll(t *testing.T) {
	if egressNote("all") == "" {
		t.Error("egressNote(all) = \"\", want the host-network exclusion")
	}
	for _, mode := range []string{"restricted", "https", ""} {
		if note := egressNote(mode); note != "" {
			t.Errorf("egressNote(%q) = %q, want empty", mode, note)
		}
	}
}
