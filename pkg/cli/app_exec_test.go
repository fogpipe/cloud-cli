package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// execServer plays the API's side of an exec session: it reads the caller's
// frames until the StdinEOF control, then runs serve, which writes output and
// says how the command ended.
func execServer(t *testing.T, serve func(ws *websocket.Conn, stdin []byte)) *websocket.Conn {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		var stdin []byte
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if mt == websocket.BinaryMessage {
				stdin = append(stdin, data...)
				continue
			}
			var ctl client.ExecControl
			require.NoError(t, json.Unmarshal(data, &ctl))
			if ctl.StdinEOF {
				break
			}
		}
		serve(ws, stdin)
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_, _, _ = ws.ReadMessage()
	}))
	t.Cleanup(srv.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { ws.Close() })
	return ws
}

func intPtr(n int) *int { return &n }

// A caller whose stdin is already at EOF still gets the command's output and a
// clean exit: the EOF is a control frame, not the end of the session.
func TestRelayExecOutputArrivesAfterStdinEOF(t *testing.T) {
	ws := execServer(t, func(ws *websocket.Conn, stdin []byte) {
		assert.Empty(t, stdin)
		require.NoError(t, ws.WriteMessage(websocket.BinaryMessage, []byte("uid=1001\n")))
		require.NoError(t, ws.WriteJSON(client.ExecControl{ExitCode: intPtr(0)}))
	})
	var out bytes.Buffer
	require.NoError(t, relayExec(ws, strings.NewReader(""), &out))
	assert.Equal(t, "uid=1001\n", out.String())
}

// Input is forwarded as bytes, and its end is told to the server.
func TestRelayExecForwardsStdin(t *testing.T) {
	ws := execServer(t, func(ws *websocket.Conn, stdin []byte) {
		assert.Equal(t, "hello\n", string(stdin))
		require.NoError(t, ws.WriteJSON(client.ExecControl{ExitCode: intPtr(0)}))
	})
	var out bytes.Buffer
	require.NoError(t, relayExec(ws, strings.NewReader("hello\n"), &out))
}

// The command's exit status is the CLI's, as with ssh and kubectl exec.
func TestRelayExecCarriesTheCommandsExitStatus(t *testing.T) {
	ws := execServer(t, func(ws *websocket.Conn, stdin []byte) {
		require.NoError(t, ws.WriteMessage(websocket.BinaryMessage, []byte("hi\n")))
		require.NoError(t, ws.WriteJSON(client.ExecControl{ExitCode: intPtr(3)}))
	})
	var out bytes.Buffer
	err := relayExec(ws, strings.NewReader(""), &out)
	assert.Equal(t, "hi\n", out.String())
	assert.Equal(t, 3, exitCode(err))
}

// A command the platform could not run is an error naming why, exit 1.
func TestRelayExecReportsAFailureToRun(t *testing.T) {
	ws := execServer(t, func(ws *websocket.Conn, stdin []byte) {
		require.NoError(t, ws.WriteJSON(client.ExecControl{Error: "container not found"}))
	})
	err := relayExec(ws, strings.NewReader(""), io.Discard)
	require.EqualError(t, err, "container not found")
	assert.Equal(t, exitFailure, exitCode(err))
}

// A session closed before the command said how it ended is a dropped session,
// never a success — and it is transient, so the caller may retry.
func TestRelayExecSessionDroppedIsUnavailable(t *testing.T) {
	ws := execServer(t, func(ws *websocket.Conn, stdin []byte) {})
	err := relayExec(ws, strings.NewReader(""), io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ended before the command did")
	assert.Equal(t, exitUnavailable, exitCode(err))
}
