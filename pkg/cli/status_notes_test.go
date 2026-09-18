package cli

import (
	"testing"
	"time"

	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/stretchr/testify/assert"
)

// A problem line carries its age when the record has one, and nothing when it
// does not; the count is shown only above one (fogpipe/cloud-workspace#1015).
func TestProblemNotes_CarryTheAge(t *testing.T) {
	old := time.Now().Add(-3 * 24 * time.Hour).UTC().Format(time.RFC3339)
	notes := problemNotes([]client.StatusProblem{
		{Reason: "Error", Detail: "container app exited with code 1 and was restarted", Count: 1, Since: old},
		{Reason: "OOMKilled", Detail: "container app ran out of memory", Count: 3},
		{Reason: "Unschedulable", Since: "not a time"},
	})
	assert.Equal(t, []string{
		"Error — container app exited with code 1 and was restarted, 3d ago",
		"OOMKilled — container app ran out of memory (×3)",
		"Unschedulable",
	}, notes)
}
