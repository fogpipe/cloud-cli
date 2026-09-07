package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// "3 of 4" is unambiguous where a bare 4 under any heading is not; the most
// the pool may run is what the ceiling admits when that is lower
// (fogpipe/cloud-workspace#146).
func TestRunnerBusy_IsBusyBesideTheMost(t *testing.T) {
	require.Equal(t, "3 of 4", runnerBusy(&client.Runner{RunningRunners: 3, MaxRunners: 4}))
	require.Equal(t, "2 of 2", runnerBusy(&client.Runner{RunningRunners: 2, MaxRunners: 4, AdmittedRunners: 2}))
}

// The queue is read or it is unreadable; it is never rendered as empty
// because it could not be read (docs/reading-a-zero.md).
func TestRunnerWaiting_ReadOrUnreadableNeverEmptyByDefault(t *testing.T) {
	at := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	require.Contains(t, runnerWaiting(&client.Runner{Queue: &client.RunnerQueue{Assigned: 14, Running: 3, Waiting: 11, AsOf: at}}), "11 job(s) assigned by GitHub and not started")
	require.Contains(t, runnerWaiting(&client.Runner{Queue: &client.RunnerQueue{AsOf: at}}), "none (as of")
	unread := runnerWaiting(&client.Runner{Problems: []client.StatusProblem{{Reason: "QueueUnread", Detail: "the listener for acme-ci publishes no gha_assigned_jobs yet"}}})
	require.Contains(t, unread, "unreadable")
	require.Contains(t, unread, "publishes no gha_assigned_jobs")
	require.Contains(t, runnerWaiting(&client.Runner{}), "not reported")
	require.Contains(t, runnerWaitingCell(&client.Runner{}), "?")
	require.Equal(t, "0", runnerWaitingCell(&client.Runner{Queue: &client.RunnerQueue{}}))
}
