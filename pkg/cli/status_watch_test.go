package cli

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// An expired login must not wedge the watch: once credentials are good again the
// next poll has to succeed, without restarting the process. The watch used to
// pin its client — and with it the ID token — for the life of the process, so an
// hour-old watch failed forever and re-running `fpcloud login` could not reach it.
func TestWatchPollRecoversWhenCredentialsBecomeValid(t *testing.T) {
	var valid atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !valid.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"UNAUTHORIZED","message":"invalid or expired credentials"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"project":{"id":"p1","name":"demo"},"apps":[],"observed_at":"2026-08-11T10:00:00Z"}`))
	}))
	defer srv.Close()

	_, _, err := client.New(srv.URL, "stale-token").ProjectStatus(t.Context(), "p1", "")
	require.Error(t, err)
	assert.Contains(t, pollFailure(err), "fpcloud login",
		"a 401 must tell the watcher what to do, since the view keeps retrying rather than exiting")

	valid.Store(true)
	status, _, err := client.New(srv.URL, "fresh-token").ProjectStatus(t.Context(), "p1", "")
	require.NoError(t, err, "a rebuilt client must pick the new credentials up")
	assert.Equal(t, "demo", status.Project.Name)
}

// A watch opened on the current project follows `fpcloud switch`; one opened on
// a named project does not. The watch used to capture the project once, so the
// prompt segment and the view on the same screen disagreed about what current
// meant after a switch.
func TestWatchProjectSourceFollowsSwitchUnlessPinned(t *testing.T) {
	t.Setenv("FPCLOUD_CONFIG_DIR", t.TempDir())
	require.NoError(t, saveConfig(&Config{CurrentProject: "before"}))

	current := projectSource(nil)
	got, err := current()
	require.NoError(t, err)
	assert.Equal(t, "before", got)

	require.NoError(t, saveConfig(&Config{CurrentProject: "after"}))
	got, err = current()
	require.NoError(t, err)
	assert.Equal(t, "after", got, "the no-argument form re-reads the context each tick")

	named := projectSource([]string{"pinned"})
	got, err = named()
	require.NoError(t, err)
	assert.Equal(t, "pinned", got)

	flag := rootCmd.Flag("project")
	require.NoError(t, flag.Value.Set("flagged"))
	flag.Changed = true
	t.Cleanup(func() { flag.Changed = false })
	flagged := projectSource(nil)
	require.NoError(t, saveConfig(&Config{CurrentProject: "elsewhere"}))
	got, err = flagged()
	require.NoError(t, err)
	assert.Equal(t, "flagged", got, "--project names a thing and stays pinned")

	require.NoError(t, saveConfig(&Config{}))
	flag.Changed = false
	_, err = projectSource(nil)()
	assert.ErrorContains(t, err, "fpcloud switch", "a context with no project says what to do")
}
