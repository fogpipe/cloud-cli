package cli

import (
	"testing"

	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A deploy must never write into a prefix another deploy already used (#475).
//
// Counting from the live version does exactly that after a failed publish: the
// upload landed in v3, the flip failed, the site is still on v2, and the retry
// targets v3 again — a second build written over the first. With --keep-extra
// the two trees merge and the version stops being a snapshot of one deploy,
// which is the property rollback rests on.
func TestNextWebsiteVersion(t *testing.T) {
	assert.Equal(t, 4, nextWebsiteVersion([]int{1, 2, 3}, 2),
		"v3 is an abandoned upload — burn the number rather than overwrite it")
	assert.Equal(t, 4, nextWebsiteVersion([]int{1, 2, 3}, 3))
	assert.Equal(t, 1, nextWebsiteVersion(nil, 0), "a site's first deploy is v1")
	assert.Equal(t, 8, nextWebsiteVersion([]int{5, 6}, 7),
		"the pointer can run ahead of the store after a partly-failed flip (#554)")
	assert.Equal(t, 11, nextWebsiteVersion([]int{9, 10}, 0),
		"a pointer that says nothing is deployed does not make old prefixes free")
}

func published(version int, retained bool) *client.WebsiteVersion {
	return &client.WebsiteVersion{Version: version, Retained: retained}
}

// A bare `website rollback` returns to the previous PUBLISHED version, not to
// live-minus-one: version numbers have gaps wherever a publish failed, and the
// number below the live one is as likely to be an abandoned upload.
func TestPreviousWebsiteVersion(t *testing.T) {
	versions := []*client.WebsiteVersion{published(4, true), published(2, true), published(1, true)}
	assert.Equal(t, 2, previousWebsiteVersion(versions, 4), "v3 was never published")

	pruned := []*client.WebsiteVersion{published(4, true), published(2, false), published(1, true)}
	assert.Equal(t, 1, previousWebsiteVersion(pruned, 4), "v2's files are gone; skip to one that can serve")

	assert.Equal(t, 0, previousWebsiteVersion([]*client.WebsiteVersion{published(1, true)}, 1),
		"a site on its first version has nowhere to go back to")
}

// The two ways a rollback target can be unusable are different facts and must
// read as different problems.
func TestCheckRollbackTarget(t *testing.T) {
	versions := []*client.WebsiteVersion{published(4, true), published(2, false), published(1, true)}

	require.NoError(t, checkRollbackTarget(versions, 4))

	err := checkRollbackTarget(versions, 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never published")

	err = checkRollbackTarget(versions, 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no longer retained")
}
