package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// A drain names what it is waiting for, so a restart held by a long job is
// not mistaken for a hang (fogpipe/cloud-workspace#145).
func TestRunnerRestartNote_NamesWhatTheDrainWaitsFor(t *testing.T) {
	r := &client.Runner{Restart: &client.RunnerRestart{RequestedAt: time.Now(), Waiting: []client.RunnerInstance{
		{Name: "ci-abc-runner-1", State: "busy", Job: "test-web", Repository: "acme/shop", StartedAt: time.Now().Add(-4 * time.Minute)},
	}}}

	note := runnerRestartNote(r)

	require.Contains(t, note, "draining: waiting for 1 running job(s)")
	require.Contains(t, note, `"test-web" on acme/shop`)
	require.Equal(t, "restart complete", runnerRestartNote(&client.Runner{}))
	require.Equal(t, "listener replaced, waiting for the new one to register", runnerRestartNote(&client.Runner{Restart: &client.RunnerRestart{Note: "listener replaced, waiting for the new one to register"}}))
}

func TestRestartMode(t *testing.T) {
	require.Contains(t, restartMode(false), "drain")
	require.Contains(t, restartMode(true), "killed")
}
