package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A client behind the control plane whose docs it reads is told so; a level,
// newer or dev client is not (fogpipe/cloud-workspace#351).
func TestDocsSkewNote(t *testing.T) {
	require.Contains(t, docsSkewNote("v0.79.2", "v0.80.0"), "control plane v0.80.0; this client is v0.79.2")
	require.Empty(t, docsSkewNote("v0.80.0", "v0.80.0"))
	require.Empty(t, docsSkewNote("v0.81.0", "v0.80.0"))
	require.Empty(t, docsSkewNote("dev", "v0.80.0"))
	require.Empty(t, docsSkewNote("v0.80.0", ""))
}
