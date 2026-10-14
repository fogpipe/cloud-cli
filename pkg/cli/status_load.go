package cli

import (
	"fmt"
	"strings"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// loadLines renders a resource's measured use as two lines, cpu then memory, each
// the average per replica, the requested percentile if any, the peak, and the
// limit the app declared with how much of it the average is — in columns, so
// the two read as one table. The cpu line carries the window's caveats: how
// much of it the record covered when that is less than asked, and the step
// when it is coarser than a minute.
//
// Nothing without a load block: the document carries one only when asked, so
// an app without one under --load is the store not answering, which the
// unchecked list names — that is said on the line rather than left as a row
// that merely lacks two lines.
func loadLines(l *client.Load, sizing string, suggestions bool, unchecked []client.UncheckedStatus) []string {
	if l == nil {
		for _, u := range unchecked {
			if u.Check == "load" {
				return []string{"cpu     not read — " + u.Error, "memory  not read — " + u.Error}
			}
		}
		return nil
	}
	rows := [][]string{
		append([]string{"cpu"}, loadAxisCells(l.CPU, l, formatMillicores)...),
		append([]string{"memory"}, loadAxisCells(l.Memory, l, humanizeSize)...),
	}
	// The two lines are one small table: every cell padded to its column
	// across both, so avg sits under avg and the share under the share.
	widths := []int{}
	for _, r := range rows {
		for i, c := range r {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], len([]rune(c)))
		}
	}
	lines := make([]string, 0, 2)
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			pad := strings.Repeat(" ", widths[i]-len([]rune(c)))
			if i == len(widths)-1 && len(r) == len(widths) {
				cells[i] = pad + c
			} else {
				cells[i] = c + pad
			}
		}
		lines = append(lines, strings.TrimRight(strings.Join(cells, "  "), " "))
	}
	if note := loadNote(l); note != "" {
		lines[0] += "   (" + note + ")"
	}
	if len(l.Series) > 0 {
		lines = append(lines,
			sparkLine("cpu    ", l.Series, l.SeriesStep, axisLimit(l.CPU), func(p client.LoadPoint) *client.LoadPointAxis { return p.CPU }, formatMillicores),
			sparkLine("memory ", l.Series, l.SeriesStep, axisLimit(l.Memory), func(p client.LoadPoint) *client.LoadPointAxis { return p.Memory }, humanizeSize))
	}
	if suggestions {
		lines = append(lines, suggestLine(l, sizing))
	}
	return lines
}

// sparkWidth is the most columns a sparkline takes: a week's series is 168
// points, which a terminal line is not.
const sparkWidth = 48

var sparkBars = []rune("▁▂▃▄▅▆▇█")

func axisLimit(a *client.LoadAxis) int64 {
	if a == nil {
		return 0
	}
	return a.Limit
}

// sparkLine draws one axis's peaks over the series against its limit: a column
// per point, or per run of points as the run's highest when there are more than
// sparkWidth, since a spike is what the line is for. A column the source held
// nothing for is "·", never the lowest bar, which is a measured near-nothing
// (docs/reading-a-zero.md). With no limit read, the scale is the series' own
// highest peak.
func sparkLine(label string, series []client.LoadPoint, step string, limit int64, axis func(client.LoadPoint) *client.LoadPointAxis, format func(int64) string) string {
	run := (len(series) + sparkWidth - 1) / sparkWidth
	type column struct {
		covered bool
		peak    int64
	}
	var cols []column
	top := limit
	for i := 0; i < len(series); i += run {
		var c column
		for _, p := range series[i:min(i+run, len(series))] {
			if !p.Covered {
				continue
			}
			c.covered = true
			if a := axis(p); a != nil {
				c.peak = max(c.peak, a.Peak)
			}
		}
		if limit <= 0 {
			top = max(top, c.peak)
		}
		cols = append(cols, c)
	}
	var b strings.Builder
	for _, c := range cols {
		switch {
		case !c.covered:
			b.WriteRune('·')
		case top <= 0:
			b.WriteRune(sparkBars[0])
		default:
			i := int(float64(c.peak) / float64(top) * float64(len(sparkBars)-1))
			b.WriteRune(sparkBars[min(max(i, 0), len(sparkBars)-1)])
		}
	}
	per := step
	if run > 1 {
		per = fmt.Sprintf("%d × %s", run, step)
	}
	against := "against its highest"
	if limit > 0 {
		against = "against the " + format(limit) + " limit"
	}
	return fmt.Sprintf("%s %s  peak per %s, %s", label, b.String(), per, against)
}

// suggestLine is the limit the reading argues for, as the command that sets
// it, over what was covered. The API decides whether there is one — a day
// covered, both axes read — and when there is none the line says which
// was missing rather than leaving a flag that printed nothing.
func suggestLine(l *client.Load, sizing string) string {
	if l.Suggest == nil {
		if l.CPU == nil || l.Memory == nil {
			return "suggest —  nothing to size from: no reading on both axes"
		}
		return fmt.Sprintf("suggest —  needs a day covered, has %s", l.Covered)
	}
	from := "peak"
	if l.Percentile > 0 {
		from = fmt.Sprintf("p%d", l.Percentile)
	}
	return fmt.Sprintf("suggest fpcloud %s --cpu %s --memory %s   (cpu from the %s, memory from the peak, over %s)",
		sizing, l.Suggest.CPU, l.Suggest.Memory, from, l.Covered)
}

// loadAxisCells is one axis as columns — "10m avg", "31m p95", "48m peak",
// "of 250m", "4%" — or what an axis with no sample is. Over a window the
// source covered that is idle, which is measured; over one it holds nothing
// of it is unknown, and saying idle there is the zero that reads as healthy
// (fogpipe/cloud-workspace#1087).
func loadAxisCells(axis *client.LoadAxis, l *client.Load, format func(int64) string) []string {
	if axis == nil {
		if l.Covered == "0s" {
			return []string{"nothing recorded over " + l.Window}
		}
		return []string{"idle over " + l.Window}
	}
	cells := []string{format(axis.Avg) + " avg"}
	if l.Percentile > 0 {
		cells = append(cells, fmt.Sprintf("%s p%d", format(axis.Pct), l.Percentile))
	}
	cells = append(cells, format(axis.Peak)+" peak")
	if axis.Limit > 0 {
		cells = append(cells, "of "+format(axis.Limit), fmt.Sprintf("%.0f%%", float64(axis.Avg)/float64(axis.Limit)*100))
	}
	return cells
}

// loadNote is what the window's answer is not: shorter than asked, or coarser
// than a minute. Empty when the answer is the whole window at full resolution.
func loadNote(l *client.Load) string {
	parts := []string{}
	if l.Covered != "" && l.Covered != l.Window {
		parts = append(parts, "covered "+l.Covered+" of "+l.Window)
	}
	if l.Step != "" && l.Step != "1m" {
		parts = append(parts, l.Step+" steps")
	}
	return strings.Join(parts, ", ")
}

// formatMillicores prints cpu the way a limit is declared: millicores below a
// core, cores above it.
func formatMillicores(m int64) string {
	if m < 1000 {
		return fmt.Sprintf("%dm", m)
	}
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", float64(m)/1000), "0"), ".")
}
