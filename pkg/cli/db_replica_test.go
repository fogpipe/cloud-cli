package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/fogpipe/cloud-cli/pkg/client"
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

// The read address is the replica's, on the primary's port, and absent when the
// cluster could not be read — never the primary's host under a second label,
// which would send a query meant for the replica to the primary and quietly
// report it as a replica read (fogpipe/cloud-workspace#862).
func TestReadAddressIsTheReplicaOrNothing(t *testing.T) {
	db := &client.Database{Host: "shop-rw.fp-acme", ReadHost: "shop-ro.fp-acme", Port: 5432}
	assert.Equal(t, "shop-ro.fp-acme:5432", dbReadAddress(db))
	assert.Equal(t, "shop-rw.fp-acme:5432", dbAddress(db),
		"the accepted twin: the primary address is unchanged by this")

	unread := &client.Database{Host: "shop-rw.fp-acme", Port: 5432}
	assert.Empty(t, dbReadAddress(unread),
		"a cluster the platform could not read has no read address to name")

	noPort := &client.Database{ReadHost: "shop-ro.fp-acme"}
	assert.Equal(t, "shop-ro.fp-acme", dbReadAddress(noPort),
		"a missing port renders the host alone rather than :0")
}
