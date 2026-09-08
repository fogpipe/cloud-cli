package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// An unmeasured lag is not a lag of zero.
//
// The staleness of a read against the replica endpoint is the one thing a
// tenant cannot reason about from the docs alone, so the platform reports the
// number. A cluster that could not be asked and a replica that is caught up are
// different answers, and rendering both as "0s" makes the figure worthless —
// which is the shape `docs/reading-a-zero.md` is about
// (fogpipe/cloud-workspace#300).
func TestReplicationLagSaysUnknownRatherThanZero(t *testing.T) {
	assert.Equal(t, "unknown", renderReplicationLag(nil),
		"a lag the platform could not measure must not render as a replica that is caught up")

	zero := 0.0
	assert.Equal(t, "< 1ms", renderReplicationLag(&zero),
		"a caught-up replica is a real, distinct answer")

	sub := 0.004
	assert.Equal(t, "4ms", renderReplicationLag(&sub))

	slow := 12.5
	assert.Equal(t, "12.5s", renderReplicationLag(&slow),
		"a replica this far behind is exactly when a tenant needs to see the number")
}
