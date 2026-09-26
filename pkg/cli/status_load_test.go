package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// --load prints cpu on one line and memory on the next, each the per-replica
// average and peak over the window beside the declared limit, and the share of
// the limit the average is (fogpipe/cloud-workspace#1078).
func TestLoadLinesRenderCPUThenMemoryAgainstTheLimit(t *testing.T) {
	a := client.AppStatus{Name: "api", Load: &client.AppLoad{
		Window: "1h",
		CPU:    &client.LoadAxis{Avg: 10, Peak: 48, Limit: 250},
		Memory: &client.LoadAxis{Avg: 80 << 20, Peak: 91 << 20, Limit: 256 << 20},
	}}
	lines := loadLines(a, nil)
	assert.Equal(t, []string{
		"cpu     10m avg · 48m peak · of 250m    4%",
		"memory  80Mi avg · 91Mi peak · of 256Mi   31%",
	}, lines)
}

// An axis with no sample is a measured nothing — a serverless app scaled to
// zero — and reads idle; an app with no load block under a load read is the
// store not answering, and the line says what the unchecked entry says.
func TestLoadLinesSeparateIdleFromUnread(t *testing.T) {
	idle := client.AppStatus{Name: "web", Mode: "serverless", Load: &client.AppLoad{Window: "6h"}}
	assert.Equal(t, []string{"cpu     idle over 6h", "memory  idle over 6h"}, loadLines(idle, nil))

	unread := loadLines(client.AppStatus{Name: "api"}, []client.UncheckedStatus{{Check: "load", Error: "prometheus unreachable"}})
	assert.Len(t, unread, 2)
	assert.Contains(t, unread[0], "not read — prometheus unreachable")

	assert.Nil(t, loadLines(client.AppStatus{Name: "api"}, nil), "no load asked for, no lines")
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
		Apps: []client.AppStatus{{Name: "api", Mode: "always-on", Status: "running", Desired: 1, Ready: 1, Load: &client.AppLoad{
			Window: "1h",
			CPU:    &client.LoadAxis{Avg: 10, Peak: 48, Limit: 250},
			Memory: &client.LoadAxis{Avg: 80 << 20, Peak: 91 << 20, Limit: 256 << 20},
		}}},
	}
	out := renderProjectStatus(s, nil)
	lines := strings.Split(out, "\n")
	var row int
	for i, l := range lines {
		if strings.Contains(l, "api ") && strings.Contains(l, "always-on") {
			row = i
		}
	}
	assert.NotZero(t, row)
	assert.Contains(t, lines[row+1], "cpu     10m avg · 48m peak · of 250m")
	assert.Contains(t, lines[row+2], "memory  80Mi avg · 91Mi peak · of 256Mi")
}
