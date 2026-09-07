package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// An empty deletion table says what it leaves unsaid: the tags a tie at the
// keep_last boundary kept, and the repositories the preview could not read
// (fogpipe/cloud-workspace#283).
func TestRetentionCaveatsNameTheTieAndTheSkipped(t *testing.T) {
	seen := time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC)
	preview := &client.RetentionPreview{
		Tied: []client.RetentionPreviewItem{
			{Repo: "backend", Tag: "v1", Reason: "tied at keep_last", FirstSeenAt: &seen},
			{Repo: "backend", Tag: "v2", Reason: "tied at keep_last", FirstSeenAt: &seen},
		},
		Skipped: 1,
	}
	lines := retentionCaveats(preview)
	if assert.Len(t, lines, 2) {
		assert.Contains(t, lines[0], "backend: 2 tag(s) beyond keep_last kept")
		assert.Contains(t, lines[0], "2026-09-05 11:00")
		assert.Contains(t, lines[1], "1 repository could not be read")
	}
	assert.Empty(t, retentionCaveats(&client.RetentionPreview{}), "nothing kept by a tie and nothing skipped is silence")
}
