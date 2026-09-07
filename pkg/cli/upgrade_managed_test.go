package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A managed install is detected from the RESOLVED executable path, because both
// managers link the binary into a bin directory from somewhere else — which is
// exactly the shape that let the Homebrew case succeed silently
// (fogpipe/cloud-workspace#803).
func TestAManagedInstallIsRecognisedFromItsResolvedPath(t *testing.T) {
	for _, tc := range []struct {
		path    string
		nix     bool
		brew    bool
		comment string
	}{
		{path: "/nix/store/abc123-fpcloud-0.170.0/bin/fpcloud", nix: true},
		{path: "/opt/homebrew/Cellar/fpcloud/0.169.0/bin/fpcloud", brew: true,
			comment: "apple silicon"},
		{path: "/usr/local/Cellar/fpcloud/0.169.0/bin/fpcloud", brew: true,
			comment: "intel mac and linuxbrew"},
		{path: "/home/linuxbrew/.linuxbrew/Cellar/fpcloud/0.169.0/bin/fpcloud", brew: true},
		{path: "/usr/local/bin/fpcloud", comment: "curl | sh — upgrade owns this one"},
		{path: "/home/dev/.local/bin/fpcloud", comment: "curl | sh, unprivileged"},
		{path: "/home/dev/go/bin/fpcloud", comment: "go install — not managed, replace it"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			require.Equal(t, tc.nix, strings.HasPrefix(tc.path, nixStorePrefix), "nix")
			require.Equal(t, tc.brew, strings.Contains(tc.path, brewCellarSegment), "brew")
		})
	}
}

// The two must not both match, or the order of the checks would decide which
// manager a tenant is told to use.
func TestNoPathIsBothNixAndHomebrew(t *testing.T) {
	for _, p := range []string{
		"/nix/store/abc123-fpcloud-0.170.0/bin/fpcloud",
		"/opt/homebrew/Cellar/fpcloud/0.169.0/bin/fpcloud",
		"/usr/local/bin/fpcloud",
	} {
		nix := strings.HasPrefix(p, nixStorePrefix)
		brew := strings.Contains(p, brewCellarSegment)
		require.False(t, nix && brew, p)
	}
}

// The notice names the command and does not run it: a CLI invoking another
// package manager on someone's machine is not a decision it gets to make. Read
// back from the pure message rather than from brewUpgradeNotice, which looks up
// the latest release over the network.
//
// That indirection is here for a second reason, and it is the one to read if
// you are about to reach for a skip. This test first went through
// brewUpgradeNotice and carried
//
//	if err == nil { t.Skip("this build is already the latest release") }
//
// which is CONDITIONAL, so ADR-127's rule — an unconditional skip is refused,
// a conditional one declines because its inputs are not here — reads it as
// legitimate. It is not, and the rule is not fine-grained enough to say so.
//
// The sharper form: a skip conditional on WHAT THE TEST IS TESTING AGAINST
// declines exactly where the answer matters. This one skipped on every install
// already at the latest release — that is every machine except one carrying the
// bug, so the test would have been quiet on the only build that could fail it.
// A skip is legitimate when it names something absent from the ENVIRONMENT (no
// database, no cluster, no API key); it is not when it names a property of the
// subject.
//
// The remedy is nearly always the same one as here: make the thing under test a
// pure function of its inputs, and the condition stops existing.
func TestTheBrewNoticeNamesTheCommandRatherThanRunningIt(t *testing.T) {
	msg := brewUpgradeMessage("v0.166.1", "v0.170.0")
	require.Contains(t, msg, "brew update && brew upgrade fpcloud")
	require.Contains(t, msg, "Homebrew", "the tenant has to know WHY their own upgrade command is not the one")
	require.Contains(t, msg, "next `brew upgrade`",
		"the reason replacing it is wrong is that brew puts the old version back — say so, or this reads as a refusal to help")
	require.Contains(t, msg, "You have v0.166.1; the latest release is v0.170.0")
}

// A failed release lookup is not a reason to withhold the instruction: the
// tenant still cannot upgrade this binary in place, and the command is the same.
func TestTheBrewNoticeStillInstructsWhenTheLookupFailed(t *testing.T) {
	msg := brewUpgradeMessage("v0.166.1", "")
	require.Contains(t, msg, "brew update && brew upgrade fpcloud")
	require.Contains(t, msg, "You have v0.166.1")
	require.NotContains(t, msg, "the latest release is")
}
