package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// The runners a pool has are said by state, because a runner executing a job
// and one waiting for a pod the ceiling refuses call for opposite responses
// (fogpipe/cloud-workspace#120). A control plane that sends only the sum still
// renders.
func TestRunnerActivity(t *testing.T) {
	assert.Equal(t, "2 running, 2 pending", runnerActivity(&client.Runner{CurrentRunners: 4, RunningRunners: 2, PendingRunners: 2}))
	assert.Equal(t, "0 running, 3 pending", runnerActivity(&client.Runner{CurrentRunners: 3, PendingRunners: 3}))
	assert.Equal(t, "4 active", runnerActivity(&client.Runner{CurrentRunners: 4}), "a control plane that predates the split")
}
