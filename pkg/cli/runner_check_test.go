package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiscoverWorkflowsTakesYmlAndYamlOnly(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"ci.yml", "release.yaml", "README.md", ".keep"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("on: push\n"), 0o600))
	}
	require.NoError(t, os.Mkdir(filepath.Join(dir, "nested"), 0o755))

	got, err := discoverWorkflows(dir)
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(dir, "ci.yml"), filepath.Join(dir, "release.yaml")}, got)
}

// A repository with no workflow directory is the ordinary case for someone who
// has not written one yet, and must read as "nothing to check" rather than as
// an error about a missing directory.
func TestDiscoverWorkflowsIsEmptyWithoutTheDirectory(t *testing.T) {
	got, err := discoverWorkflows(filepath.Join(t.TempDir(), "absent"))
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestCheckPoolNoteSaysWhenThereAreNoPools(t *testing.T) {
	require.Contains(t, checkPoolNote(nil), "no runner pools")
	require.Contains(t, checkPoolNote([]string{"proj-ci"}), "proj-ci")
	require.Contains(t, checkPoolNote([]string{"proj-ci", "proj-arm"}), "2 pool labels")
}

func TestRunsOnCellNamesTheFirstLabelAndCountsTheRest(t *testing.T) {
	require.Equal(t, "—", runsOnCell(nil))
	require.Equal(t, "proj-ci", runsOnCell([]string{"proj-ci"}))
	require.Equal(t, "self-hosted (+2)", runsOnCell([]string{"self-hosted", "linux", "x64"}))
}
