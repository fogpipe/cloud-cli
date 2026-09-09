package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// A runner's label is only offered to repositories in the account the runner
// serves; the note says both, so the label never reads as usable from
// anywhere (fogpipe/cloud-workspace#305).
func TestRunnerUseNote_TiesTheLabelToTheAccount(t *testing.T) {
	note := runnerUseNote(&client.Runner{GitHubConfigURL: "https://github.com/fogpipe", Labels: []string{"travelplanner-ci"}})

	require.Contains(t, note, "github.com/fogpipe repository")
	require.Contains(t, note, "runs-on: travelplanner-ci")
	require.Contains(t, note, "queues forever")
}

func TestRepoOutsideAccount(t *testing.T) {
	require.True(t, repoOutsideAccount("ixDevAB/travelplanner", "fogpipe"))
	require.False(t, repoOutsideAccount("Fogpipe/cloud-cli", "fogpipe"), "GitHub logins are case-insensitive")
	require.False(t, repoOutsideAccount("travelplanner", "fogpipe"), "no owner is no verdict")
	require.False(t, repoOutsideAccount("ixDevAB/travelplanner", ""), "no connection is no verdict")
}
