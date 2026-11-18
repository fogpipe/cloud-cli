package cli

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// An app and a website take a firewall through their own update command, with
// the same three flags, and both wait for the edge unless told not to.
func TestAppAndWebsiteUpdateTakeTheFirewallFlags(t *testing.T) {
	for _, c := range []*cobra.Command{appUpdateCmd, websiteUpdateCmd} {
		for _, name := range []string{"firewall", "no-firewall", "no-wait"} {
			assert.NotNil(t, c.Flags().Lookup(name), "%s --%s", c.CommandPath(), name)
		}
		assert.Equal(t, "false", c.Flags().Lookup("no-wait").DefValue, c.CommandPath())
	}
	assert.Contains(t, renderLLMHelp(rootCmd), "--no-firewall")
}

func TestAppUpdateAttachesAndDetachesAFirewall(t *testing.T) {
	fastFirewallWait(t, time.Minute)
	f := newFakeFirewallAPI(t)

	require.NoError(t, runFpcloud(t, "app", "update", "web", "--firewall", "office"))
	assert.Equal(t, []client.AttachFirewallRequest{{Firewall: "office"}}, f.attached)
	require.NotNil(t, f.appRef)
	assert.Equal(t, "applied", f.appRef.Status, "the command returned only once the edge enforced it")

	require.NoError(t, runFpcloud(t, "app", "update", "web", "--no-firewall"))
	assert.Equal(t, 1, f.detached)
	assert.Nil(t, f.appRef, "the command returned only once the edge had let go")

	require.NoError(t, runFpcloud(t, "app", "update", "web", "--firewall", "office", "--no-wait"))
	assert.Equal(t, "pending", f.appRef.Status)

	err := runFpcloud(t, "app", "update", "web", "--firewall", "office", "--no-firewall")
	require.Error(t, err, "attaching and detaching in one command is refused")
	assert.Len(t, f.attached, 2)
}
