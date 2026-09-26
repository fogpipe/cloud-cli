package cli

import (
	"fmt"
	"strings"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// loadLines renders an app's measured use as two lines, cpu then memory, each
// the average and the peak per replica over the window beside the limit the
// app declared, and how much of that limit the average is.
//
// Nothing without a load block: the document carries one only when asked, so
// an app without one under --load is the store not answering, which the
// unchecked list names — that is said on the line rather than left as a row
// that merely lacks two lines.
func loadLines(a client.AppStatus, unchecked []client.UncheckedStatus) []string {
	if a.Load == nil {
		for _, u := range unchecked {
			if u.Check == "load" {
				return []string{"cpu     not read — " + u.Error, "memory  not read — " + u.Error}
			}
		}
		return nil
	}
	return []string{
		"cpu     " + loadAxisLabel(a.Load.CPU, a.Load.Window, formatMillicores),
		"memory  " + loadAxisLabel(a.Load.Memory, a.Load.Window, humanizeSize),
	}
}

// loadAxisLabel is one axis: "10m avg · 48m peak · of 250m   4%", or what an
// axis with no sample in the window is — idle, which is measured, not missing.
func loadAxisLabel(axis *client.LoadAxis, window string, format func(int64) string) string {
	if axis == nil {
		return "idle over " + window
	}
	parts := []string{format(axis.Avg) + " avg", format(axis.Peak) + " peak"}
	if axis.Limit > 0 {
		parts = append(parts, "of "+format(axis.Limit))
	}
	line := strings.Join(parts, " · ")
	if axis.Limit > 0 {
		line += fmt.Sprintf("  %3.0f%%", float64(axis.Avg)/float64(axis.Limit)*100)
	}
	return line
}

// formatMillicores prints cpu the way a limit is declared: millicores below a
// core, cores above it.
func formatMillicores(m int64) string {
	if m < 1000 {
		return fmt.Sprintf("%dm", m)
	}
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", float64(m)/1000), "0"), ".")
}
