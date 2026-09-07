package cli

import (
	"context"
	"fmt"
	"io"
	"os"

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
// way `db connect` does (ADR-045) — no kubeconfig, because FKE is an operator
// entitlement and inspecting your own app must not require one.
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

		return relayExec(cmd.Context(), ws, tty)
	},
}

// relayExec copies stdin into the session and the container's output back out,
// returning when the far end closes. The read side owns the return: the
// container decides when the command is over, and stdin may legitimately never
// reach EOF (a terminal does not).
func relayExec(ctx context.Context, ws *websocket.Conn, tty bool) error {
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				// EOF on a piped stdin is the caller saying there is no more
				// input, which the container should see as a closed stream.
				_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
		}
	}()

	for {
		_, data, err := ws.ReadMessage()
		if len(data) > 0 {
			if _, werr := os.Stdout.Write(data); werr != nil {
				return werr
			}
		}
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			// The server closes with the reason when the command itself
			// failed, so it reaches the caller rather than being swallowed.
			if ce, ok := err.(*websocket.CloseError); ok && ce.Text != "" {
				return fmt.Errorf("%s", ce.Text)
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}
