package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

func cve(id, severity string) client.RegistryCVE {
	return client.RegistryCVE{ID: id, Severity: severity}
}

var sample = []client.RegistryCVE{
	cve("CVE-1", "CRITICAL"),
	cve("CVE-2", "HIGH"),
	cve("CVE-3", "MEDIUM"),
	cve("CVE-4", "LOW"),
	cve("CVE-5", "UNKNOWN"),
}

// A floor, not a set: --min-severity high is CRITICAL and HIGH
// (fogpipe/cloud-workspace#951).
func TestMinSeverityIsAFloor(t *testing.T) {
	kept, hidden, err := filterBySeverity(sample, "high")
	require.NoError(t, err)
	assert.Equal(t, []string{"CVE-1", "CVE-2"}, ids(kept))
	assert.Equal(t, 3, hidden)
}

// UNKNOWN ranks LOWEST, so any floor above zero excludes it. That follows the
// rank the platform assigns on the way in -- an unrecognised severity is not
// evidence of danger -- and it is the one thing about this flag that is not
// obvious, which is why it is asserted rather than left to the help text.
func TestUnknownRanksBelowLow(t *testing.T) {
	kept, _, err := filterBySeverity(sample, "low")
	require.NoError(t, err)
	assert.Equal(t, []string{"CVE-1", "CVE-2", "CVE-3", "CVE-4"}, ids(kept),
		"low keeps LOW and above, and UNKNOWN is below it")

	kept, _, err = filterBySeverity(sample, "unknown")
	require.NoError(t, err)
	assert.Len(t, kept, 5, "the bottom floor keeps everything")
}

// A severity this binary does not recognise is withheld by a floor rather than
// admitted: a newer server may report a level this client predates, and letting
// it through every filter would rank it as critical.
func TestAnUnrecognisedSeverityIsWithheldNotPromoted(t *testing.T) {
	kept, hidden, err := filterBySeverity([]client.RegistryCVE{cve("CVE-X", "SEVERE")}, "low")
	require.NoError(t, err)
	assert.Empty(t, kept)
	assert.Equal(t, 1, hidden)
}

// No flag is no floor. The control: without this, a filter that dropped
// everything would be indistinguishable from the default.
func TestNoFloorShowsEverything(t *testing.T) {
	kept, hidden, err := filterBySeverity(sample, "")
	require.NoError(t, err)
	assert.Len(t, kept, 5)
	assert.Equal(t, 0, hidden)
}

// A typo is refused. Treating it as no floor reads as "nothing was filtered";
// treating it as the top floor reads as a clean image. Both are worse than an
// error naming the levels.
func TestATypoIsRefusedRatherThanGuessed(t *testing.T) {
	_, _, err := filterBySeverity(sample, "hihg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "critical")
}

// The line under the state line says what was withheld, so a short table is
// never read as a short image.
func TestTheFilterSaysWhatItHid(t *testing.T) {
	assert.Equal(t, "Showing 2 at HIGH or above; 3 hidden by --min-severity.",
		severityFilterLine("high", 2, 3))
	assert.Empty(t, severityFilterLine("", 5, 0), "an unfiltered read is unchanged")
}

func ids(cves []client.RegistryCVE) []string {
	out := make([]string, 0, len(cves))
	for _, c := range cves {
		out = append(out, c.ID)
	}
	return out
}
