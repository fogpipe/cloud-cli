package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRetention(t *testing.T) {
	for in, want := range map[string]int{
		"24h": 86400,
		"36h": 129600,
		"90m": 5400,
		"7d":  604800,
		"30d": 2592000,
		"2w":  1209600,
		// Every spelling of "no age limit" resolves to the same 0 the API takes,
		// so nobody has to know that 0 is what unlimited is called.
		"never":   0,
		"forever": 0,
		"0":       0,
		"":        0,
		"  7D  ":  604800,
	} {
		got, err := parseRetention(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	for _, bad := range []string{"soon", "-1d", "7 days", "d", "1y", "-5h"} {
		_, err := parseRetention(bad)
		assert.Error(t, err, bad)
	}
}

func TestFormatRetention(t *testing.T) {
	assert.Equal(t, "forever", formatRetention(0))
	assert.Equal(t, "7d", formatRetention(604800))
	assert.Equal(t, "30d", formatRetention(2592000))
	assert.Equal(t, "1h0m0s", formatRetention(3600))
}
