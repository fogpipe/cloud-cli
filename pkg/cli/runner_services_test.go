package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

func servicesCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	runnerServiceFlags(cmd)
	cmd.SetArgs(args)
	cmd.SetOut(nil)
	require.NoError(t, cmd.Execute())
	return cmd
}

func TestNoServiceFlagsLeavesThePoolAlone(t *testing.T) {
	got, err := runnerServicesFromFlags(servicesCmd(t))
	require.NoError(t, err)
	require.Nil(t, got, "nil is what makes create send nothing and update leave the set alone")
}

func TestServicesAreBuiltInDeclarationOrder(t *testing.T) {
	got, err := runnerServicesFromFlags(servicesCmd(t,
		"--service", "postgres=postgres:18-alpine",
		"--service", "redis=redis:7",
		"--service-env", "postgres=POSTGRES_PASSWORD=hunter2",
		"--service-env", "postgres=POSTGRES_DB=app",
		"--service-memory", "postgres=1Gi",
		"--service-cpu", "redis=250m",
	))
	require.NoError(t, err)
	require.Equal(t, []client.RunnerService{
		{
			Name:   "postgres",
			Image:  "postgres:18-alpine",
			Env:    map[string]string{"POSTGRES_PASSWORD": "hunter2", "POSTGRES_DB": "app"},
			Memory: "1Gi",
		},
		{Name: "redis", Image: "redis:7", CPU: "250m"},
	}, got)
}

// A value that carries `=` is the ordinary case for a connection string or a
// base64 blob, so only the first separator may split the pair.
func TestAServiceEnvValueMayContainEquals(t *testing.T) {
	got, err := runnerServicesFromFlags(servicesCmd(t,
		"--service", "db=postgres:18",
		"--service-env", "db=DSN=postgres://u:p@127.0.0.1/x?sslmode=disable",
	))
	require.NoError(t, err)
	require.Equal(t, "postgres://u:p@127.0.0.1/x?sslmode=disable", got[0].Env["DSN"])
}

// The whole failure mode of a flag pair keyed by a string: a modifier whose
// service was never declared silently configures nothing.
func TestAModifierForAnUndeclaredServiceIsRefused(t *testing.T) {
	_, err := runnerServicesFromFlags(servicesCmd(t,
		"--service", "postgres=postgres:18",
		"--service-env", "postgrse=POSTGRES_PASSWORD=x",
	))
	require.ErrorContains(t, err, `names service "postgrse"`)
	require.ErrorContains(t, err, "declared: postgres", "the message has to say what WAS declared, or a typo is a guessing game")
}

func TestAModifierWithNoServiceAtAllIsRefused(t *testing.T) {
	_, err := runnerServicesFromFlags(servicesCmd(t, "--service-env", "db=KEY=value"))
	require.ErrorContains(t, err, "none were declared")
}

func TestMalformedDeclarationsAreRefused(t *testing.T) {
	for _, spec := range []string{"postgres", "=postgres:18", "postgres="} {
		_, err := runnerServicesFromFlags(servicesCmd(t, "--service", spec))
		require.ErrorContains(t, err, "expected <name>=<image>", spec)
	}
}

func TestADuplicateServiceNameIsRefused(t *testing.T) {
	_, err := runnerServicesFromFlags(servicesCmd(t,
		"--service", "db=postgres:18",
		"--service", "db=postgres:17",
	))
	require.ErrorContains(t, err, "declared twice")
}

func TestServiceCellNamesEachServiceAndItsImage(t *testing.T) {
	require.Equal(t, "—", serviceCell(nil))
	require.Equal(t, "db (postgres:18)", serviceCell([]client.RunnerService{{Name: "db", Image: "postgres:18"}}))
}
