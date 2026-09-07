package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A credential handed to docker has to cover the whole push, not the next
// request: docker fetches it once and re-mints with it until the manifest lands
// (fogpipe/cloud-workspace#285).
func TestTokenNeedsRefresh(t *testing.T) {
	now := time.Date(2026, 9, 5, 14, 45, 0, 0, time.UTC)
	assert.False(t, tokenNeedsRefresh(now.Add(5*time.Minute), now, time.Minute), "a request's horizon is met")
	assert.True(t, tokenNeedsRefresh(now.Add(5*time.Minute), now, time.Hour), "a push's horizon is not")
	assert.False(t, tokenNeedsRefresh(now.Add(12*time.Hour), now, time.Hour))
	assert.True(t, tokenNeedsRefresh(now.Add(-time.Second), now, time.Minute))
}
