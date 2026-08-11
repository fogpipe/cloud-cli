package cli

import (
	"context"
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

var webhookCmd = &cobra.Command{
	Use:     "webhook",
	Aliases: []string{"webhooks"},
	Short:   "Manage GitHub webhook auto-deploy",
}

var webhookSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set up GitHub webhook for auto-deploy",
	RunE: func(cmd *cobra.Command, args []string) error {
		appRef, _ := cmd.Flags().GetString("app")
		if appRef == "" {
			return fmt.Errorf("--app is required")
		}
		repo, _ := cmd.Flags().GetString("repo")
		if repo == "" {
			return fmt.Errorf("--repo is required")
		}
		imagePattern, _ := cmd.Flags().GetString("image")
		if imagePattern == "" {
			return fmt.Errorf("--image is required (e.g. ghcr.io/owner/repo:{{sha}})")
		}
		branch, _ := cmd.Flags().GetString("branch")

		outputFormat := rootCmd.Flag("output").Value.String()
		c := getClient()
		appID, err := resolveAppID(c, appRef)
		if err != nil {
			return err
		}

		var wh *client.AppWebhook
		var setupErr error
		action := func() {
			wh, setupErr = c.SetupWebhook(context.Background(), appID, client.SetupWebhookRequest{
				Repo:         repo,
				Branch:       branch,
				ImagePattern: imagePattern,
			})
		}

		if !isStructured(outputFormat) {
			withSpinner("Setting up webhook...", action)
		} else {
			action()
		}
		if setupErr != nil {
			return setupErr
		}

		if isStructured(outputFormat) {
			return renderData(wh)
		}

		// Show the webhook URL and secret prominently.
		secretStyle := lipgloss.NewStyle().Bold(true).Foreground(colorWarning)
		urlStyle := lipgloss.NewStyle().Bold(true).Foreground(colorInfo)

		fmt.Println(renderInfoBox("Webhook Configured", [][]string{
			{"Repo", wh.Repo},
			{"Branch", wh.Branch},
			{"Image Pattern", wh.ImagePattern},
			{"Webhook URL", urlStyle.Render(wh.WebhookURL)},
			{"Secret", secretStyle.Render(wh.WebhookSecret)},
		}))

		fmt.Println()
		infoStyle := lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorWarning).
			Padding(0, 2)
		fmt.Println(infoStyle.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorWarning).Render("Setup Instructions") + "\n\n" +
				"1. Go to your repo Settings > Webhooks > Add webhook\n" +
				"2. Set Payload URL to the Webhook URL above\n" +
				"3. Set Content type to application/json\n" +
				"4. Set Secret to the secret above\n" +
				"5. Select \"Just the push event\"\n" +
				"6. Save\n\n" +
				mutedStyle.Render("The secret is only shown once. Store it securely."),
		))

		return nil
	},
}

var webhookStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show webhook configuration and status",
	RunE: func(cmd *cobra.Command, args []string) error {
		appRef, _ := cmd.Flags().GetString("app")
		if appRef == "" {
			return fmt.Errorf("--app is required")
		}

		c := getClient()
		appID, err := resolveAppID(c, appRef)
		if err != nil {
			return err
		}
		wh, err := c.GetWebhook(context.Background(), appID)
		if err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(wh)
		}

		lastDeploy := mutedStyle.Render("never")
		if wh.LastDeployAt != nil {
			lastDeploy = *wh.LastDeployAt
		}
		lastSHA := mutedStyle.Render("none")
		if wh.LastDeploySHA != "" {
			lastSHA = wh.LastDeploySHA
			if len(lastSHA) > 12 {
				lastSHA = lastSHA[:12]
			}
		}

		enabledStr := renderStatus("running")
		if !wh.Enabled {
			enabledStr = renderStatus("stopped")
		}

		fmt.Println(renderInfoBox("Webhook Status", [][]string{
			{"Repo", wh.Repo},
			{"Branch", wh.Branch},
			{"Image Pattern", wh.ImagePattern},
			{"Enabled", enabledStr},
			{"Webhook URL", lipgloss.NewStyle().Bold(true).Foreground(colorInfo).Render(wh.WebhookURL)},
			{"Last Deploy", lastDeploy},
			{"Last SHA", lastSHA},
		}))

		return nil
	},
}

var webhookRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove webhook configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		appRef, _ := cmd.Flags().GetString("app")
		if appRef == "" {
			return fmt.Errorf("--app is required")
		}

		c := getClient()
		appID, err := resolveAppID(c, appRef)
		if err != nil {
			return err
		}

		var removeErr error
		outputFormat := rootCmd.Flag("output").Value.String()
		action := func() {
			removeErr = c.RemoveWebhook(context.Background(), appID)
		}

		if !isStructured(outputFormat) {
			withSpinner("Removing webhook...", action)
		} else {
			action()
		}
		if removeErr != nil {
			return removeErr
		}

		if isStructured(outputFormat) {
			return renderData(map[string]string{"status": "removed"})
		}

		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				" Webhook removed.",
		))
		return nil
	},
}

func init() {
	webhookSetupCmd.Flags().String("app", "", "App name or ID (required)")
	webhookSetupCmd.Flags().String("repo", "", "GitHub repository (e.g. owner/repo)")
	webhookSetupCmd.Flags().String("branch", "main", "Branch to deploy on push")
	webhookSetupCmd.Flags().String("image", "", "Image pattern (e.g. ghcr.io/owner/repo:{{sha}})")

	webhookStatusCmd.Flags().String("app", "", "App name or ID (required)")

	webhookRemoveCmd.Flags().String("app", "", "App name or ID (required)")

	webhookCmd.AddCommand(webhookSetupCmd, webhookStatusCmd, webhookRemoveCmd)
	rootCmd.AddCommand(webhookCmd)
}
