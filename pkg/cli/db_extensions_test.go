package cli

import (
	"testing"

	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/stretchr/testify/assert"
)

// An update that says nothing about extensions removes none — the whole set is
// replaced only when --extension was given.
func TestNoOpinionRemovesNothing(t *testing.T) {
	assert.Empty(t, removedExtensions([]string{"semver"}, client.UpdateDatabaseRequest{}))
}

// What the new set no longer names is what gets dropped, which is what the
// confirmation has to name (fogpipe/cloud-workspace#295).
func TestRemovedExtensionsAreWhatTheNewSetDropped(t *testing.T) {
	keep := []string{"semver"}
	assert.Equal(t, []string{"pg_statecharts"},
		removedExtensions([]string{"semver", "pg_statecharts"}, client.UpdateDatabaseRequest{Extensions: &keep}))

	none := []string{}
	assert.Equal(t, []string{"semver", "pg_statecharts"},
		removedExtensions([]string{"semver", "pg_statecharts"}, client.UpdateDatabaseRequest{Extensions: &none}))

	added := []string{"semver", "vector"}
	assert.Empty(t, removedExtensions([]string{"semver"}, client.UpdateDatabaseRequest{Extensions: &added}))
}
