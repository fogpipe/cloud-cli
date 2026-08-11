package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var githubCmd = &cobra.Command{
	Use:   "github",
	Short: "Connect this project to a GitHub account",
	Long: `Connect this project to the GitHub account its runners serve.

Connecting records which GitHub account this project may register runners on.
You authorize as yourself, and only accounts you administer can be connected —
Fogpipe never takes an organization name on trust, so there is nothing to type
and nothing to spoof.

If the Fogpipe app is not installed on the account yet, connecting tells you and
gives you the link.

  fpcloud github connect
  fpcloud runner create ci

Runner pools then serve every repository in the connected account.`,
}

var githubConnectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Install the Fogpipe GitHub App and connect an account",
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

		fmt.Println("Opening GitHub to connect your account…")
		fmt.Println()
		fmt.Println("  " + start.URL)
		fmt.Println()
		// Printed as well as opened: this runs over SSH and in containers often
		// enough that a silent no-op would look like the command hung.
		_ = openBrowser(start.URL)
		fmt.Println("Authorize as yourself, then run:")
		fmt.Println("  fpcloud github status")
		return nil
	},
}

var githubStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show which GitHub account this project is connected to",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := requireProject()
		if err != nil {
			return err
		}
		conn, err := getClient().GetGitHubConnection(context.Background(), project)
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(conn)
		}
		fmt.Println(renderInfoBox("GitHub", [][]string{
			{"Account", conn.AccountLogin},
			{"Type", conn.AccountType},
			{"Installation", conn.InstallationID},
			{"Connected by", conn.ConnectedBy},
		}))
		fmt.Println()
		return nil
	},
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
	githubConnectCmd.Flags().String("account", "", "Which GitHub account to connect, if you administer more than one")
	githubCmd.AddCommand(githubConnectCmd, githubStatusCmd, githubDisconnectCmd)
	rootCmd.AddCommand(githubCmd)
}
