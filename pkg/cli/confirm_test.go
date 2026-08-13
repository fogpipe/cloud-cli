package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The reported case: `fpcloud storage keys delete … < /dev/null`, which is how a
// CI job rotating keys runs it. /dev/null is a character device, so a mode check
// calls it a terminal and huh goes on to open /dev/tty and fail with an error
// that names neither the command nor the flag.
func TestConfirmWithoutATerminalNamesTheFlag(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	require.NoError(t, err)
	defer devNull.Close()

	ok, err := confirmOn(devNull, "Revoke access key?", "Cannot be undone.", "Yes, revoke")

	assert.False(t, ok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--yes")
	assert.NotContains(t, strings.ToLower(err.Error()), "tty")
}

func TestNotATerminalWhenRedirected(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	require.NoError(t, err)
	defer devNull.Close()

	assert.False(t, isTerminal(devNull))
}
