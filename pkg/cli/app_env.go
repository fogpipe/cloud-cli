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
	Short: "Manage an app's environment variables",
	Long: `Set, list and remove the environment variables an app runs with. Every
change rolls the app onto it.

  fpcloud app env set web LOG_LEVEL=info
  fpcloud app env set web DATABASE_URL --from-secret maindb-owner
  fpcloud app env set web STRIPE_KEY_FILE --from-secret stripe-key --path /secrets/stripe
  fpcloud app env list web
  fpcloud app env unset web LOG_LEVEL

A literal reads back in full. A credential belongs in a project secret
(` + "`fpcloud secret`" + `), referenced from a variable: the variable holds the
secret's value, or, with --path, the file at that path holds it and the
variable holds the path. Referencing a secret hands the app's identity the
resource behind it. Org-wide secret bundles mirrored into projects are
` + "`fpcloud secrets`" + `, a different thing.`,
}

var appEnvSetCmd = &cobra.Command{
	Use:   "set <app> KEY=VALUE | KEY --from-secret <secret> [--path /file]",
	Short: "Set an environment variable, literal or from a secret",
	Long: `Set an environment variable for an app and roll the app onto it.

KEY=VALUE sets a literal. KEY --from-secret <secret> makes the variable hold
the secret's value; with --path the file at that path holds the value and the
variable holds the path. A reference hands the app's identity the resource
behind the secret: the grant is written with it when you may make it, and the
write is refused naming the grant when you may not.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		appRef := args[0]
		secret, _ := cmd.Flags().GetString("from-secret")
		path, _ := cmd.Flags().GetString("path")

		var key, value string
		if secret != "" {
			if strings.Contains(args[1], "=") {
				return fmt.Errorf("--from-secret takes the variable's name alone: KEY --from-secret %s", secret)
			}
			key = args[1]
		} else {
			if path != "" {
				return fmt.Errorf("--path goes with --from-secret: the file holds a secret's value")
			}
			parts := strings.SplitN(args[1], "=", 2)
			if len(parts) != 2 || parts[0] == "" {
				return fmt.Errorf("invalid format: use KEY=VALUE, or KEY --from-secret <secret>")
			}
			key, value = parts[0], parts[1]
		}

		outputFormat := rootCmd.Flag("output").Value.String()

		c := getClient()
		appID, err := resolveAppID(c, appRef)
		if err != nil {
			return err
		}
		var cfg interface{}
		var setErr error
		action := func() {
			if secret != "" {
				cfg, setErr = c.SetSecretEnv(context.Background(), appID, key, secret, path)
			} else {
				cfg, setErr = c.SetConfig(context.Background(), appID, key, value)
			}
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

		what := fmt.Sprintf(" Variable %q set for app %s", key, appRef)
		switch {
		case secret != "" && path != "":
			what = fmt.Sprintf(" Variable %q names %s, the file holding secret %s, for app %s", key, path, secret, appRef)
		case secret != "":
			what = fmt.Sprintf(" Variable %q holds secret %s for app %s", key, secret, appRef)
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") + what,
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
			value := cfg.Value
			if cfg.SecretRef != nil {
				value = mutedStyle.Render("secret " + cfg.SecretRef.Secret)
				if cfg.SecretRef.Path != "" {
					value = cfg.SecretRef.Path + " " + mutedStyle.Render("(file holding secret "+cfg.SecretRef.Secret+")")
				}
			}
			rows[i] = []string{cfg.Key, value}
		}
		render([]string{"KEY", "VALUE"}, rows, configs)
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
	appEnvSetCmd.Flags().String("from-secret", "", "Make the variable hold a project secret's value (a database's <db>-owner, a bucket's <bucket>-key, or one you created)")
	appEnvSetCmd.Flags().String("path", "", "With --from-secret: deliver the value as a file at this path, and make the variable hold the path")
	appEnvCmd.AddCommand(appEnvSetCmd, appEnvListCmd, appEnvUnsetCmd)
	appCmd.AddCommand(appEnvCmd)
}
