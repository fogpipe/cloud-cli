package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// httpLines renders what the edge served an app or a website over the load
// window: the requests by status class with the 5xx codes that say who failed
// beside them, the bytes, then app time and edge time at p50, p95 and p99.
// A resource the edge cannot see says why, one that could not be read says
// so, and a window nothing was recorded over is never read as no requests
// (docs/reading-a-zero.md).
func httpLines(h *client.HTTPLoad, unchecked []client.UncheckedStatus) []string {
	if h == nil {
		for _, u := range unchecked {
			if u.Check == "http" {
				return []string{"http    not read — " + u.Error}
			}
		}
		return nil
	}
	if h.Unmeasured != "" {
		return []string{"http    not measured: a " + h.Unmeasured + " app is not served through the edge"}
	}
	if h.Requests == nil || h.Requests.Total == 0 {
		if h.Covered == "0s" {
			return []string{"http    nothing recorded over " + h.Window}
		}
		line := "http    no requests over " + h.Window
		if note := httpNote(h); note != "" {
			line += "   (" + note + ")"
		}
		return []string{line}
	}
	line := "http    " + countLabel(h.Requests.Total, "request") + ": " + statusClasses(h.Requests)
	if h.Bytes != nil {
		line += fmt.Sprintf(" · in %s, out %s", humanizeSize(h.Bytes.Request), humanizeSize(h.Bytes.Response))
	}
	if note := httpNote(h); note != "" {
		line += "   (" + note + ")"
	}
	lines := []string{line}
	if l := latencyLine(h); l != "" {
		lines = append(lines, l)
	}
	if len(h.Series) > 0 {
		lines = append(lines, httpSparkLine(h.Series, h.SeriesStep))
	}
	return lines
}

// statusClasses is each class that served anything, 5xx with its codes:
// "2xx 1180, 4xx 20, 5xx 4 (502 3, 500 1)".
func statusClasses(c *client.HTTPStatusCounts) string {
	parts := []string{}
	for _, cl := range []struct {
		name string
		n    int64
	}{{"1xx", c.C1xx}, {"2xx", c.C2xx}, {"3xx", c.C3xx}, {"4xx", c.C4xx}, {"5xx", c.C5xx}} {
		if cl.n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", cl.name, cl.n))
		}
	}
	if c.C5xx > 0 {
		codes := []string{}
		for _, code := range []struct {
			name string
			n    int64
		}{{"500", c.S500}, {"502", c.S502}, {"503", c.S503}, {"504", c.S504}} {
			if code.n > 0 {
				codes = append(codes, fmt.Sprintf("%s %d", code.name, code.n))
			}
		}
		if other := c.C5xx - c.S500 - c.S502 - c.S503 - c.S504; other > 0 {
			codes = append(codes, fmt.Sprintf("other %d", other))
		}
		parts[len(parts)-1] += " (" + strings.Join(codes, ", ") + ")"
	}
	return strings.Join(parts, ", ")
}

// latencyLine is app time then edge time; empty when no request was timed.
func latencyLine(h *client.HTTPLoad) string {
	parts := []string{}
	for _, l := range []struct {
		name string
		lat  *client.HTTPLatency
	}{{"app", h.AppTime}, {"edge", h.EdgeTime}} {
		if l.lat == nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s p50 %s p95 %s p99 %s", l.name,
			formatQuantile(l.lat.P50), formatQuantile(l.lat.P95), formatQuantile(l.lat.P99)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "latency " + strings.Join(parts, " · ")
}

// formatQuantile is a percentile in the unit that reads: milliseconds under a
// second, seconds above, and past the edge's top bucket only that it is above
// it, never the bound as if it were the latency.
func formatQuantile(q client.HTTPQuantile) string {
	v := formatMs(q.Ms)
	if q.Above {
		return ">" + v
	}
	return v
}

func formatMs(ms float64) string {
	if ms < 1000 {
		return strconv.FormatFloat(ms, 'f', -1, 64) + "ms"
	}
	return strconv.FormatFloat(ms/1000, 'f', -1, 64) + "s"
}

// httpNote is what the window's answer is not, as loadNote says it for load.
func httpNote(h *client.HTTPLoad) string {
	parts := []string{}
	if h.Covered != "" && h.Covered != h.Window {
		parts = append(parts, "covered "+h.Covered+" of "+h.Window)
	}
	if h.Step != "" && h.Step != "1m" {
		parts = append(parts, h.Step+" steps")
	}
	return strings.Join(parts, ", ")
}

// httpSparkLine draws the requests of each step against the busiest, a column
// per point or per run of points as the run's total; a column the source held
// nothing for is "·", never the lowest bar.
func httpSparkLine(series []client.HTTPPoint, step string) string {
	run := (len(series) + sparkWidth - 1) / sparkWidth
	type column struct {
		covered bool
		n       int64
	}
	var cols []column
	var top int64
	for i := 0; i < len(series); i += run {
		var c column
		for _, p := range series[i:min(i+run, len(series))] {
			if !p.Covered {
				continue
			}
			c.covered = true
			if p.Requests != nil {
				c.n += p.Requests.Total
			}
		}
		top = max(top, c.n)
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
			i := int(float64(c.n) / float64(top) * float64(len(sparkBars)-1))
			b.WriteRune(sparkBars[min(max(i, 0), len(sparkBars)-1)])
		}
	}
	per := step
	if run > 1 {
		per = fmt.Sprintf("%d × %s", run, step)
	}
	return fmt.Sprintf("http    %s  requests per %s, against the busiest (%d)", b.String(), per, top)
}

func countLabel(n int64, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
