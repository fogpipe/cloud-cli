package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A scratch directory goes on the wire as a typed block, and clearing sends an
// empty list rather than null, which the API refuses as a missing field.
func TestSetAppVolumeMountsSendsTypedBlocksAndAnEmptyListToClear(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/api/v1/apps/app-1/volume-mounts", r.URL.Path)
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodies = append(bodies, string(b))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"app-1","name":"web","volume_mounts":[{"mount_path":"/tmp","empty_dir":{}}]}`))
	}))
	defer server.Close()

	c := New(server.URL, "test-key")
	app, err := c.SetAppVolumeMounts(context.Background(), "app-1", []VolumeMount{{MountPath: "/tmp", EmptyDir: &EmptyDir{}}})
	require.NoError(t, err)
	require.Len(t, app.VolumeMounts, 1)
	assert.Equal(t, "/tmp", app.VolumeMounts[0].MountPath)
	assert.NotNil(t, app.VolumeMounts[0].EmptyDir)

	_, err = c.SetAppVolumeMounts(context.Background(), "app-1", nil)
	require.NoError(t, err)

	require.Len(t, bodies, 2)
	assert.JSONEq(t, `{"volume_mounts":[{"mount_path":"/tmp","empty_dir":{}}]}`, bodies[0])
	assert.JSONEq(t, `{"volume_mounts":[]}`, bodies[1])
}
