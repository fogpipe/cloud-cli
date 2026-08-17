package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRestoreFlags(t *testing.T) {
	t.Run("platform restore needs a target", func(t *testing.T) {
		err := validateRestoreFlags(false, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--target is required")
	})

	t.Run("platform restore into a target", func(t *testing.T) {
		require.NoError(t, validateRestoreFlags(false, "mydb-recovered", ""))
		require.NoError(t, validateRestoreFlags(false, "mydb-jul10", "2026-07-10T14:30:00Z"))
	})

	t.Run("external alone", func(t *testing.T) {
		require.NoError(t, validateRestoreFlags(true, "", ""))
	})

	t.Run("external refuses a target", func(t *testing.T) {
		err := validateRestoreFlags(true, "mydb-recovered", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "in place")
		assert.Contains(t, err.Error(), "mydb-recovered")
	})

	t.Run("external refuses a point in time", func(t *testing.T) {
		err := validateRestoreFlags(true, "", "2026-07-10T14:30:00Z")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "point in time")
	})
}
