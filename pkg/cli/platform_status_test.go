package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The deploy time is the sentence that ends a hunt: shown as written and as
// an age (fogpipe/cloud-workspace#144).
func TestRenderPlatformStatus_SaysWhenThePlatformChanged(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 13, 0, 0, time.UTC)
	out := renderPlatformStatus(serverVersion{Version: "v0.143.0", MinClientVersion: "v0.140.0", DeployedAt: "2026-09-07T09:07:00Z"}, "v0.142.0", now)

	require.Contains(t, out, "v0.143.0")
	require.Contains(t, out, "2026-09-07T09:07:00Z (1h 6m ago)")
	require.Contains(t, out, "v0.140.0")
	require.Contains(t, out, "v0.142.0")
}

// A control plane that does not say when it was deployed is reported as not
// saying — never as a blank that reads like "never deployed".
func TestRenderPlatformStatus_AnUnknownDeployTimeIsSaidToBeUnknown(t *testing.T) {
	out := renderPlatformStatus(serverVersion{Version: "v0.143.0", MinClientVersion: "v0.140.0"}, "dev", time.Now())

	require.Contains(t, out, "not reported by this control plane")
}

func TestHumanDuration(t *testing.T) {
	require.Equal(t, "less than a minute", humanDuration(20*time.Second))
	require.Equal(t, "45 min", humanDuration(45*time.Minute))
	require.Equal(t, "3h 5m", humanDuration(3*time.Hour+5*time.Minute))
	require.Equal(t, "4 days", humanDuration(4*24*time.Hour+time.Hour))
}
