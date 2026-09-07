package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// A failed run exits non-zero whatever was printed about it: structured output
// exists for scripts, and a script gating on `job run --wait -o json` must not
// go green on failure (fogpipe/cloud-workspace#333).
func TestRunOutcome_FailedRunIsAnErrorOnEveryOutputPath(t *testing.T) {
	assert.Error(t, runOutcome(&client.JobRun{RunName: "sweep-1", Status: "failed"}))
	assert.NoError(t, runOutcome(&client.JobRun{RunName: "sweep-2", Status: "succeeded"}))
}
