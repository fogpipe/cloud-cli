package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseKeyValues(t *testing.T) {
	got, err := parseKeyValues([]string{"HOST_URL=https://x.example", "TOKEN=a=b"}, "--env")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"HOST_URL": "https://x.example", "TOKEN": "a=b"}, got)

	empty, err := parseKeyValues(nil, "--env")
	require.NoError(t, err)
	assert.Nil(t, empty)

	_, err = parseKeyValues([]string{"HOST_URL"}, "--env")
	assert.Error(t, err)

	_, err = parseKeyValues([]string{"=v"}, "--env")
	assert.Error(t, err)

	_, err = parseKeyValues([]string{"K=1", "K=2"}, "--secret")
	assert.Error(t, err)
}
