package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// --load prints cpu on one line and memory on the next, each the per-replica
// average, the percentile when one was asked for, the peak and the declared
// limit with the share of it the average is; the cpu line says when the record
// covered less than the window asked, and when the step is coarser than a
// minute (fogpipe/cloud-workspace#1078, #1083).
func TestLoadLinesRenderCPUThenMemoryAgainstTheLimit(t *testing.T) {
	a := client.AppStatus{Name: "api", Load: &client.Load{
		Window: "1h", Step: "1m", Covered: "1h",
		CPU:    &client.LoadAxis{Avg: 10, Peak: 48, Limit: 250},
		Memory: &client.LoadAxis{Avg: 80 << 20, Peak: 91 << 20, Limit: 256 << 20},
	}}
	assert.Equal(t, []string{
		"cpu     10m avg   48m peak   of 250m    4%",
		"memory  80Mi avg  91Mi peak  of 256Mi  31%",
	}, loadLines(a.Load, "app scale x", false, nil))

	week := client.AppStatus{Name: "api", Load: &client.Load{
		Window: "7d", Step: "1h", Covered: "3d11h", Percentile: 95,
		CPU:    &client.LoadAxis{Avg: 10, Pct: 31, Peak: 48, Limit: 250},
		Memory: &client.LoadAxis{Avg: 80 << 20, Pct: 88 << 20, Peak: 91 << 20, Limit: 256 << 20},
	}}
	assert.Equal(t, []string{
		"cpu     10m avg   31m p95   48m peak   of 250m    4%   (covered 3d11h of 7d, 1h steps)",
		"memory  80Mi avg  88Mi p95  91Mi peak  of 256Mi  31%",
	}, loadLines(week.Load, "app scale x", false, nil))

}

// An axis with no sample is a measured nothing — a serverless app scaled to
// zero — and reads idle; an app with no load block under a load read is the
// store not answering, and the line says what the unchecked entry says.
func TestLoadLinesSeparateIdleFromUnread(t *testing.T) {
	idle := client.AppStatus{Name: "web", Mode: "serverless", Load: &client.Load{Window: "6h", Step: "1m", Covered: "6h"}}
	assert.Equal(t, []string{"cpu     idle over 6h", "memory  idle over 6h"}, loadLines(idle.Load, "app scale x", false, nil))

	unknown := client.AppStatus{Name: "web", Load: &client.Load{Window: "1w", Step: "1h", Covered: "0s"}}
	assert.Equal(t, []string{"cpu     nothing recorded over 1w   (covered 0s of 1w, 1h steps)", "memory  nothing recorded over 1w"}, loadLines(unknown.Load, "app scale x", false, nil),
		"a window the source holds nothing of is unknown, never idle (#1087)")

	unread := loadLines(nil, "app scale api", false, []client.UncheckedStatus{{Check: "load", Error: "prometheus unreachable"}})
	assert.Len(t, unread, 2)
	assert.Contains(t, unread[0], "not read — prometheus unreachable")

	assert.Nil(t, loadLines(nil, "app scale api", false, nil), "no load asked for, no lines")
}

func TestFormatMillicoresSpeaksLikeADeclaredLimit(t *testing.T) {
	assert.Equal(t, "10m", formatMillicores(10))
	assert.Equal(t, "999m", formatMillicores(999))
	assert.Equal(t, "1", formatMillicores(1000))
	assert.Equal(t, "1.5", formatMillicores(1500))
	assert.Equal(t, "2.25", formatMillicores(2250))
}

// The lines sit under the app row, indented past the name column, in the
// status view itself.
func TestProjectStatusRendersLoadUnderTheApp(t *testing.T) {
	s := &client.ProjectStatus{
		Project: client.StatusProject{Name: "demo", Namespace: "acme-demo", Egress: "open"},
		Apps: []client.AppStatus{{Name: "api", Mode: "always-on", Status: "running", Desired: 1, Ready: 1, Load: &client.Load{
			Window: "1h", Step: "1m", Covered: "1h",
			CPU:    &client.LoadAxis{Avg: 10, Peak: 48, Limit: 250},
			Memory: &client.LoadAxis{Avg: 80 << 20, Peak: 91 << 20, Limit: 256 << 20},
		}}},
	}
	out := renderProjectStatus(s, nil, false)
	lines := strings.Split(out, "\n")
	var row int
	for i, l := range lines {
		if strings.Contains(l, "api ") && strings.Contains(l, "always-on") {
			row = i
		}
	}
	assert.NotZero(t, row)
	assert.Contains(t, lines[row+1], "cpu     10m avg   48m peak   of 250m")
	assert.Contains(t, lines[row+2], "memory  80Mi avg  91Mi peak  of 256Mi")
}

// --series draws each axis's peaks against its limit under the two lines. A
// step the source held nothing for is "·" and a covered step no pod ran in is
// the lowest bar, so no reading never reads as idle; a series longer than a
// line is drawn a run of points per column, as the run's highest, so a spike
// survives (ADR-282).
func TestLoadLinesDrawTheSeriesAgainstTheLimit(t *testing.T) {
	at := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	point := func(i int, covered bool, cpu, mem int64) client.LoadPoint {
		p := client.LoadPoint{Start: at.Add(time.Duration(i) * 15 * time.Minute), Covered: covered}
		if covered && cpu >= 0 {
			p.CPU = &client.LoadPointAxis{Avg: cpu / 2, Peak: cpu}
			p.Memory = &client.LoadPointAxis{Avg: mem / 2, Peak: mem}
		}
		return p
	}
	l := &client.Load{
		Window: "1h", Step: "1m", Covered: "1h",
		CPU:        &client.LoadAxis{Avg: 60, Peak: 250, Limit: 250},
		Memory:     &client.LoadAxis{Avg: 64 << 20, Peak: 128 << 20, Limit: 256 << 20},
		SeriesStep: "15m",
		Series:     []client.LoadPoint{point(0, true, -1, 0), point(1, true, 125, 128<<20), point(2, true, 250, 256<<20), point(3, false, 0, 0)},
	}
	lines := loadLines(l, "app scale api", false, nil)
	assert.Equal(t, "cpu     ▁▄█·  peak per 15m, against the 250m limit", lines[2])
	assert.Equal(t, "memory  ▁▄█·  peak per 15m, against the 256Mi limit", lines[3])

	var week []client.LoadPoint
	for i := 0; i < 168; i++ {
		cpu := int64(10)
		if i == 100 {
			cpu = 250
		}
		week = append(week, point(i, true, cpu, 1<<20))
	}
	l.SeriesStep, l.Series = "1h", week
	cpu := loadLines(l, "app scale api", false, nil)[2]
	assert.Contains(t, cpu, "peak per 4 × 1h")
	bars := strings.Fields(cpu)[1]
	assert.Equal(t, 42, len([]rune(bars)), "168 points, four to a column")
	assert.Equal(t, 1, strings.Count(bars, "█"), "the one spike survives its run")
}
