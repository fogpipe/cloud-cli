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

// Each address is the one the server names for it, on the primary's port:
// the read address is never the primary's host under a second label, which
// would send a query meant for the replica to the primary and quietly report it
// as a replica read, and a database with no pooler shows no pool address.
func TestEachAddressIsTheOneTheServerNames(t *testing.T) {
	db := &client.Database{
		Host: "fpdb--shop.fp-acme.svc.cluster.local", ReadHost: "fpdb-ro--shop.fp-acme.svc.cluster.local",
		PoolHost: "fpdb-pool--shop.fp-acme.svc.cluster.local", Port: 5432,
	}
	assert.Equal(t, "fpdb--shop.fp-acme.svc.cluster.local:5432", dbAddress(db))
	assert.Equal(t, "fpdb-ro--shop.fp-acme.svc.cluster.local:5432", dbReadAddress(db))
	assert.Equal(t, "fpdb-pool--shop.fp-acme.svc.cluster.local:5432", dbPoolAddress(db))

	bare := &client.Database{Host: "fpdb--shop.fp-acme.svc.cluster.local", Port: 5432}
	assert.Empty(t, dbReadAddress(bare), "an answer naming no read address shows none")
	assert.Empty(t, dbPoolAddress(bare), "a pooler that is off has no address")

	noPort := &client.Database{ReadHost: "fpdb-ro--shop.fp-acme", PoolHost: "fpdb-pool--shop.fp-acme"}
	assert.Equal(t, "fpdb-ro--shop.fp-acme", dbReadAddress(noPort),
		"a missing port renders the host alone rather than :0")
	assert.Equal(t, "fpdb-pool--shop.fp-acme", dbPoolAddress(noPort))
}

// The pool address is a row of its own only while there is one.
func TestThePoolAddressIsShownOnlyWhileThePoolerIsOn(t *testing.T) {
	labels := func(db *client.Database) []string {
		var out []string
		for _, row := range dbDetailRows(db, dbAddress(db)) {
			out = append(out, row[0])
		}
		return out
	}
	on := &client.Database{Name: "shop", Host: "fpdb--shop.fp-acme", PoolHost: "fpdb-pool--shop.fp-acme", Port: 5432, Pooler: true}
	assert.Contains(t, labels(on), "Pool address")
	off := &client.Database{Name: "shop", Host: "fpdb--shop.fp-acme", Port: 5432}
	assert.NotContains(t, labels(off), "Pool address")
}
