package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// A runner's label is only offered to repositories in the scope the runner
// serves; the note says both, so the label never reads as usable from
// anywhere (fogpipe/cloud-workspace#305).
func TestRunnerUseNote_TiesTheLabelToTheAccount(t *testing.T) {
	note := runnerUseNote(&client.Runner{GitHubConfigURL: "https://github.com/fogpipe", Labels: []string{"travelplanner-ci"}})

	require.Contains(t, note, "github.com/fogpipe repository")
	require.Contains(t, note, "runs-on: travelplanner-ci")
	require.Contains(t, note, "queues forever")
}

// A repository-scoped runner serves exactly one repository, so the note must
// not offer it to an account (fogpipe/cloud-workspace#972).
func TestRunnerUseNote_ARepositoryScopeSaysOneRepository(t *testing.T) {
	note := runnerUseNote(&client.Runner{GitHubConfigURL: "https://github.com/lorentzlasson/grannsnack", Labels: []string{"grannsnack-ci"}})

	require.Contains(t, note, "github.com/lorentzlasson/grannsnack with")
	require.Contains(t, note, "any other repository")
	require.NotContains(t, note, "account's repository", "the account is not what this runner serves")
}

func TestRepoOutsideRunnerScope_Account(t *testing.T) {
	require.True(t, repoOutsideRunnerScope("ixDevAB/travelplanner", "fogpipe"))
	require.False(t, repoOutsideRunnerScope("Fogpipe/cloud-cli", "fogpipe"), "GitHub logins are case-insensitive")
	require.False(t, repoOutsideRunnerScope("travelplanner", "fogpipe"), "no owner is no verdict")
	require.False(t, repoOutsideRunnerScope("ixDevAB/travelplanner", ""), "no runner scope is no verdict")
}

// A repository scope admits exactly itself. A sibling in the same account is
// outside it, which is the case an account-shaped check gets wrong.
func TestRepoOutsideRunnerScope_Repository(t *testing.T) {
	require.False(t, repoOutsideRunnerScope("lorentzlasson/grannsnack", "lorentzlasson/grannsnack"))
	require.False(t, repoOutsideRunnerScope("LorentzLasson/GrannSnack", "lorentzlasson/grannsnack"), "case-insensitive")
	require.True(t, repoOutsideRunnerScope("lorentzlasson/other", "lorentzlasson/grannsnack"), "a sibling repository is still outside a repository scope")
}
