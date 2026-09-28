package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hardeningCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().Int64("run-as-user", -1, "")
	cmd.Flags().Int64("run-as-group", -1, "")
	cmd.Flags().Int64("fs-group", -1, "")
	cmd.Flags().Bool("run-as-non-root", false, "")
	cmd.Flags().Bool("read-only-root-fs", false, "")
	require.NoError(t, cmd.Flags().Parse(args))
	return cmd
}

// --run-as-non-root=false is how the root-image refusal says to opt out, and it
// equals the flag's default, so it has to be sent because it was given, not
// because its value differs (fogpipe/cloud-workspace#1112).
func TestSecurityContextFromFlags_SendsAnExplicitRootOptOut(t *testing.T) {
	sc := securityContextFromFlags(hardeningCmd(t, "--run-as-non-root=false"))
	require.NotNil(t, sc)
	assert.False(t, sc.RunAsNonRoot)

	assert.Nil(t, securityContextFromFlags(hardeningCmd(t)), "no hardening flag leaves the image default")
}
