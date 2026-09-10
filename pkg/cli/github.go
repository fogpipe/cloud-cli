package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

var githubCmd = &cobra.Command{
	Use:   "github",
	Short: "Connect this project to a GitHub organization",
	Long: `Connect this project to the GitHub organization its runners serve.

Connecting records which GitHub organization this project may register runners
on. You authorize as yourself, and only organizations you own can be connected —
Fogpipe never takes an organization name on trust, so there is nothing to type
and nothing to spoof.

A personal GitHub account cannot be connected: GitHub manages a personal
account's self-hosted runners per repository, and the Fogpipe app can only
register them on an organization. Move the repository into an organization to
run Fogpipe CI on it.

If the Fogpipe app is not installed on the organization yet, connecting tells
you and gives you the link.

  fpcloud github connect
  fpcloud runner create

The runner then serves every repository in the connected organization.`,
}

var githubConnectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Install the Fogpipe GitHub App and connect an organization",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := requireProject()
		if err != nil {
			return err
		}
		c := getClient()
		start, err := c.StartGitHubConnect(context.Background(), project, mustString(cmd, "account"))
		if err != nil {
			return err
		}

		started := time.Now()
		fmt.Println("Opening GitHub to connect your account…")
		fmt.Println()
		fmt.Println("  " + start.URL)
		fmt.Println()
		// Printed as well as opened: this runs over SSH and in containers often
		// enough that a silent no-op would look like the command hung.
		_ = openBrowser(start.URL)
		if noWait, _ := cmd.Flags().GetBool("no-wait"); noWait {
			fmt.Println("Authorize as yourself, then run:")
			fmt.Println("  fpcloud github status")
			return nil
		}
		// The outcome is decided in the callback after the browser round trip,
		// and used to reach only the browser: the terminal got "not connected"
		// and no reason (fogpipe/cloud-workspace#307). The platform records
		// it now, so wait for the record and print it.
		fmt.Println("Authorize as yourself in the browser; waiting for the outcome (Ctrl-C to stop waiting, `fpcloud github status` reads it later)…")
		attempt, err := awaitGitHubConnect(cmd.Context(), c, project, started)
		if err != nil {
			return err
		}
		return printGitHubConnectOutcome(attempt)
	},
}

// connectWaitLimit is how long `connect` waits for the browser round trip. The
// state the flow carries expires after fifteen minutes, so there is nothing
// to wait for past that.
const connectWaitLimit = 15 * time.Minute

// awaitGitHubConnect polls the recorded outcome until one newer than the start
// of this flow appears.
func awaitGitHubConnect(ctx context.Context, c *client.Client, project string, started time.Time) (*client.GitHubConnectAttempt, error) {
	deadline := time.Now().Add(connectWaitLimit)
	for {
		status, err := c.GetGitHubConnection(ctx, project)
		if err != nil {
			return nil, err
		}
		if a := status.LastAttempt; a != nil && a.AttemptedAt.After(started) {
			return a, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("no outcome was recorded within %s; the link has expired — run `fpcloud github connect` again", connectWaitLimit)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// printGitHubConnectOutcome says what the recorded attempt decided, and exits
// non-zero on a refusal so a script sees it.
func printGitHubConnectOutcome(a *client.GitHubConnectAttempt) error {
	if a.Outcome == "connected" {
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") + " Connected."))
		return nil
	}
	return fmt.Errorf("the connection was refused: %s", a.Reason)
}

var githubStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show which GitHub organization this project is connected to",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := requireProject()
		if err != nil {
			return err
		}
		status, err := getClient().GetGitHubConnection(context.Background(), project)
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(status)
		}
		if conn := status.Connection; conn != nil {
			fmt.Println(renderInfoBox("GitHub", [][]string{
				{"Account", conn.AccountLogin},
				{"Type", conn.AccountType},
				{"Installation", conn.InstallationID},
				{"Connected by", conn.ConnectedBy},
			}))
		} else {
			fmt.Println(mutedStyle.Render("  This project is not connected to GitHub."))
		}
		// The latest attempt is part of the answer: "not connected" because
		// nobody tried and because the attempt just failed read the same until
		// the outcome was recorded (fogpipe/cloud-workspace#307).
		if a := status.LastAttempt; a != nil && a.Outcome == "failed" {
			fmt.Println()
			fmt.Println(lipgloss.NewStyle().Foreground(colorWarning).Render(
				fmt.Sprintf("  Last attempt (%s%s) was refused: %s",
					a.AttemptedAt.Local().Format("2006-01-02 15:04"), byWhom(a.AttemptedBy), a.Reason)))
		}
		fmt.Println()
		return nil
	},
}

func byWhom(who string) string {
	if who == "" {
		return ""
	}
	return " by " + who
}

var githubDisconnectCmd = &cobra.Command{
	Use:   "disconnect",
	Short: "Remove this project's GitHub connection",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := requireProject()
		if err != nil {
			return err
		}
		if err := getClient().DisconnectGitHub(context.Background(), project); err != nil {
			return err
		}
		fmt.Println("Disconnected. The Fogpipe app is still installed on GitHub — remove it there to revoke access entirely.")
		return nil
	},
}

func init() {
	githubConnectCmd.Flags().String("account", "", "Which GitHub organization to connect, if you own more than one; checked before the browser opens")
	githubConnectCmd.Flags().Bool("no-wait", false, "Print the link and exit instead of waiting for the outcome")
	githubCmd.AddCommand(githubConnectCmd, githubStatusCmd, githubDisconnectCmd)
	rootCmd.AddCommand(githubCmd)
}
