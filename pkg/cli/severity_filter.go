package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// Asking for the findings that matter (fogpipe/cloud-workspace#951).
//
// A real image reports hundreds of CVEs -- 502 on the first one the platform
// ever scanned, of which 13 are critical -- and a table nobody opens is the
// failure #321 was filed for, reached by volume instead of by absence.

// severityRanks orders what the scanner reports. These are the ranks
// cvesFromTrivy assigns on the way in, and they have to agree: UNKNOWN is
// LOWEST, deliberately, because an unrecognised severity is not evidence of
// danger and ranking it above LOW would put noise above the findings somebody
// is trying to read.
//
// The consequence is worth stating out loud rather than leaving to be
// discovered: --min-severity low excludes UNKNOWN.
var severityRanks = map[string]int{
	"CRITICAL": 4,
	"HIGH":     3,
	"MEDIUM":   2,
	"LOW":      1,
	"UNKNOWN":  0,
}

// severityFloor resolves the flag to a rank. An empty value is no floor at all,
// which is the default and shows everything.
//
// An unrecognised level is REFUSED rather than treated as no floor: a typo that
// silently showed every finding would be read as "nothing was filtered", and a
// typo that silently showed none would be read as a clean image. Both are worse
// than an error naming the levels.
func severityFloor(level string) (int, bool, error) {
	if level == "" {
		return 0, false, nil
	}
	rank, ok := severityRanks[strings.ToUpper(level)]
	if !ok {
		names := make([]string, 0, len(severityRanks))
		for n := range severityRanks {
			names = append(names, strings.ToLower(n))
		}
		sort.Slice(names, func(i, j int) bool {
			return severityRanks[strings.ToUpper(names[i])] > severityRanks[strings.ToUpper(names[j])]
		})
		return 0, false, fmt.Errorf("unknown severity %q: one of %s", level, strings.Join(names, ", "))
	}
	return rank, true, nil
}

// filterBySeverity keeps the CVEs at or above the floor, and reports how many it
// withheld.
//
// The hidden count is returned rather than discarded because the caller owes it
// to the reader. Every path that prints a filtered list also prints the total
// and what was left out: `scanStateLine` says "no vulnerabilities found" when it
// is handed an empty list, and that one line is what stands between an image
// nobody scanned and an image that looks clean (#902). A filter able to empty
// the list is a filter able to forge that sentence.
func filterBySeverity(cves []client.RegistryCVE, level string) ([]client.RegistryCVE, int, error) {
	floor, ok, err := severityFloor(level)
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		return cves, 0, nil
	}
	kept := make([]client.RegistryCVE, 0, len(cves))
	for _, cve := range cves {
		// An unrecognised severity ranks 0, the same as UNKNOWN, so a level this
		// binary predates is withheld by any floor above zero rather than
		// admitted as though it were critical.
		if severityRanks[strings.ToUpper(cve.Severity)] >= floor {
			kept = append(kept, cve)
		}
	}
	return kept, len(cves) - len(kept), nil
}

// severityFilterLine says what the filter withheld, under the scan-state line.
// Empty when nothing was filtered, so an unfiltered read is unchanged.
func severityFilterLine(level string, shown, hidden int) string {
	if hidden == 0 && level == "" {
		return ""
	}
	return fmt.Sprintf("Showing %d at %s or above; %d hidden by --min-severity.",
		shown, strings.ToUpper(level), hidden)
}

// minSeverityUsage is the flag's help, shared so both commands describe it the
// same way -- they answer the same question about an image (#934).
const minSeverityUsage = "Only CVEs at this severity or above: critical, high, medium, low, unknown (unknown ranks lowest)"
