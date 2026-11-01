package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// `fpcloud app exec` — run a command in one of a running app's containers
// (fogpipe/cloud-workspace#317).
//
// A tenant could read logs and events and ask a running container nothing, so
// anything the app does not already print was unanswerable: whether the pod
// runs as the uid the image declares, whether the mounted config is what they
// think, whether this pod can reach that database. It streams over the API the
// way `db connect` does (ADR-045) — no kubeconfig, because a tenant has no
// access to the cluster and inspecting your own app goes through the API.
//
// Deliberately not a deploy mechanism: it changes nothing the platform tracks,
// and a change made inside a container is gone at the next rollout. Every
// invocation is audited with its argv (never its output, which is the
// container's and may be anything).
var appExecCmd = &cobra.Command{
	Use:   "exec <app> -- <command> [args...]",
	Short: "Run a command in a running app's container",
	Long: "Run a command inside one of the app's running containers and stream it here.\n\n" +
		"Everything after `--` is the command, passed to the container as written:\n" +
		"nothing is split, quoted or interpolated on the way. Requires app write,\n" +
		"the same as a deploy, and every invocation is audited.\n\n" +
		"For looking, not for changing: a change made in a container is gone at the\n" +
		"next rollout.",
	Example: "  fpcloud app exec api -- id\n" +
		"  fpcloud app exec api -- sh -c 'cat /etc/config/app.yaml'\n" +
		"  fpcloud app exec api --tty -- sh",
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		command := args[1:]
		if cmd.ArgsLenAtDash() > 0 {
			command = args[cmd.ArgsLenAtDash():]
		}
		if len(command) == 0 {
			return fmt.Errorf("a command is required: fpcloud app exec %s -- <command>", args[0])
		}

		c := getClient()
		appID, err := resolveAppID(c, args[0])
		if err != nil {
			return err
		}
		container, _ := cmd.Flags().GetString("container")
		tty, _ := cmd.Flags().GetBool("tty")
		// A TTY only means anything when there is one on this side; asking for
		// one without it produces a session whose control characters nothing
		// interprets.
		if tty && !isTerminal(os.Stdin) {
			return fmt.Errorf("--tty needs a terminal on stdin")
		}

		ws, err := c.DialAppExec(cmd.Context(), appID, command, container, tty)
		if err != nil {
			return err
		}
		defer ws.Close()

		if tty {
			state, err := term.MakeRaw(int(os.Stdin.Fd()))
			if err != nil {
				return fmt.Errorf("put the terminal in raw mode: %w", err)
			}
			defer func() { _ = term.Restore(int(os.Stdin.Fd()), state) }()
		}

		return relayExec(ws, os.Stdin, os.Stdout)
	},
}

// relayExec copies stdin into the session as binary frames and the container's
// output back out, and returns how the command ended. The read side owns the
// return: the container decides when the command is over, and stdin may
// legitimately never reach EOF (a terminal does not).
//
// Stdin reaching EOF is told to the server as an ExecControl frame, never as
// closing the session: a caller with no input — a script, an agent, `</dev/null`
// — is the common shape of a non-interactive run, and closing the session on
// it raced the command's first output and won.
// The server answers the command's end with an ExecControl carrying its exit
// status, which becomes this process's own; a session that closes without one
// ended before the command did, and says so.
func relayExec(ws *websocket.Conn, stdin io.Reader, stdout io.Writer) error {
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdin.Read(buf)
			if n > 0 {
				if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = ws.WriteJSON(client.ExecControl{StdinEOF: true})
				return
			}
		}
	}()

	var ended *client.ExecControl
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			switch {
			case ended == nil:
				return fmt.Errorf("the exec session ended before the command did: %w", err)
			case ended.Error != "":
				return fmt.Errorf("%s", ended.Error)
			case ended.ExitCode != nil && *ended.ExitCode != 0:
				return execExitError{code: *ended.ExitCode}
			}
			return nil
		}
		switch mt {
		case websocket.TextMessage:
			var ctl client.ExecControl
			if jerr := json.Unmarshal(data, &ctl); jerr != nil {
				return fmt.Errorf("unreadable control frame from the exec session: %w", jerr)
			}
			ended = &ctl
		case websocket.BinaryMessage:
			if _, werr := stdout.Write(data); werr != nil {
				return werr
			}
		}
	}
}

// execExitError is the exit status of a command run in a container. The CLI
// exits with it, as ssh and `kubectl exec` do, so a script branches on the
// command's own status rather than on whether the CLI managed to run it.
type execExitError struct{ code int }

func (e execExitError) Error() string {
	return fmt.Sprintf("command exited with status %d", e.code)
}
