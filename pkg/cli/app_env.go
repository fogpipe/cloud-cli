package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// appEnvCmd is the app's environment: the config store ADR-046 made the app's
// only env layer. It lives under `app`, where Heroku's `config:set -a` and
// Cloud Run's `--set-env-vars`/`--update-secrets` put it, because a top-level
// `config` reads as configuring the CLI to anyone who has used gcloud, and
// sat beside `secrets`, which is the org's bundles and not this
// (fogpipe/cloud-workspace#361).
var appEnvCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage an app's environment variables and secrets",
	Long: `Set, list and remove the environment variables an app runs with. A value
set with --secret is encrypted at rest and hidden from list. Every change
rolls the app onto it.

  fpcloud app env set web LOG_LEVEL=info
  fpcloud app env set web STRIPE_KEY=sk_live_... --secret
  fpcloud app env list web
  fpcloud app env unset web LOG_LEVEL

Org-wide secret bundles mirrored into projects are ` + "`fpcloud secrets`" + `, a
different thing.`,
}

var appEnvSetCmd = &cobra.Command{
	Use:   "set <app> KEY=VALUE",
	Short: "Set an environment variable",
	Long:  "Set an environment variable for an app and roll the app onto it. Use --secret to encrypt at rest.",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		appRef := args[0]

		parts := strings.SplitN(args[1], "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return fmt.Errorf("invalid format: use KEY=VALUE")
		}
		key := parts[0]
		value := parts[1]

		isSecret, _ := cmd.Flags().GetBool("secret")
		outputFormat := rootCmd.Flag("output").Value.String()

		c := getClient()
		appID, err := resolveAppID(c, appRef)
		if err != nil {
			return err
		}
		var cfg interface{}
		var setErr error
		action := func() {
			cfg, setErr = c.SetConfig(context.Background(), appID, key, value, isSecret)
		}

		if !isStructured(outputFormat) {
			withSpinner("Setting variable...", action)
		} else {
			action()
		}
		if setErr != nil {
			return setErr
		}

		if isStructured(outputFormat) {
			return renderData(cfg)
		}

		label := "Variable"
		if isSecret {
			label = "Secret"
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" %s %q set for app %s", label, key, appRef),
		))
		return nil
	},
}

var appEnvListCmd = &cobra.Command{
	Use:     "list <app>",
	Short:   "List an app's environment variables",
	Aliases: []string{"ls"},
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appRef := args[0]

		c := getClient()
		appID, err := resolveAppID(c, appRef)
		if err != nil {
			return err
		}
		configs, err := c.ListConfig(context.Background(), appID)
		if err != nil {
			return err
		}

		rows := make([][]string, len(configs))
		for i, cfg := range configs {
			secretLabel := ""
			if cfg.IsSecret {
				secretLabel = lipgloss.NewStyle().Foreground(colorWarning).Render("secret")
			} else {
				secretLabel = mutedStyle.Render("plain")
			}
			rows[i] = []string{cfg.Key, cfg.Value, secretLabel}
		}
		render([]string{"KEY", "VALUE", "TYPE"}, rows, configs)
		return nil
	},
}

var appEnvUnsetCmd = &cobra.Command{
	Use:   "unset <app> KEY",
	Short: "Remove an environment variable",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		appRef, key := args[0], args[1]

		c := getClient()
		appID, err := resolveAppID(c, appRef)
		if err != nil {
			return err
		}
		if err := c.UnsetConfig(context.Background(), appID, key); err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(map[string]string{"status": "removed", "key": key})
		}

		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Variable %q removed from app %s", key, appRef),
		))
		return nil
	},
}

func init() {
	appEnvSetCmd.Flags().Bool("secret", false, "Encrypt the value at rest and hide it from list")
	appEnvCmd.AddCommand(appEnvSetCmd, appEnvListCmd, appEnvUnsetCmd)
	appCmd.AddCommand(appEnvCmd)
}
