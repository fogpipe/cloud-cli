package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A config.yaml that cannot be parsed refuses every command. Dropping the error
// made a broken file indistinguishable from an absent one, and an absent one
// means "the production control plane" on a released binary
// (fogpipe/cloud-workspace#333).
func TestRefuseBrokenConfig(t *testing.T) {
	dir := isolateState(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("api_url: [not: yaml"), 0o600))

	_, err := loadConfig()
	require.Error(t, err)
	refused := refuseBrokenConfig(err)
	require.Error(t, refused)
	assert.Contains(t, refused.Error(), "config.yaml")
	assert.True(t, errors.Is(refused, err), "the parse error travels with the refusal")

	assert.NoError(t, refuseBrokenConfig(nil))
}
