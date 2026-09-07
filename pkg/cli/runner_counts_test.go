package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// A pool with runners pending reads differently from one with them all
// working, because the two call for opposite responses
// (fogpipe/cloud-workspace#120).
func TestRunnerCounts_SeparatesRunningFromPending(t *testing.T) {
	require.Equal(t, "2 running, 2 pending", runnerCounts(4, 2, 2))
	require.Equal(t, "4 running, 0 pending", runnerCounts(4, 4, 0))
	require.Equal(t, "0 running, 3 pending", runnerCounts(3, 0, 3))
}

// A control plane that sends only the sum still renders, and an empty pool
// reads as empty rather than as "0 running, 0 pending".
func TestRunnerCounts_FallsBackToTheSum(t *testing.T) {
	require.Equal(t, "4 active", runnerCounts(4, 0, 0))
	require.Equal(t, "0 active", runnerCounts(0, 0, 0))
}

// The pool view and the project-status view read the same three numbers
// through the same renderer.
func TestRunnerActivity_UsesTheSharedRenderer(t *testing.T) {
	require.Equal(t, "1 running, 2 pending",
		runnerActivity(&client.Runner{CurrentRunners: 3, RunningRunners: 1, PendingRunners: 2}))
}
