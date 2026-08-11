package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage app config and secrets",
}

var configSetCmd = &cobra.Command{
	Use:   "set KEY=VALUE",
	Short: "Set a config variable",
	Long:  "Set an environment variable for an app. Use --secret to encrypt at rest.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appRef, _ := cmd.Flags().GetString("app")
		if appRef == "" {
			return fmt.Errorf("--app is required")
		}

		parts := strings.SplitN(args[0], "=", 2)
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
			withSpinner("Setting config...", action)
		} else {
			action()
		}
		if setErr != nil {
			return setErr
		}

		if isStructured(outputFormat) {
			return renderData(cfg)
		}

		label := "Config"
		if isSecret {
			label = "Secret"
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" %s %q set for app %s", label, key, appID),
		))
		return nil
	},
}

var configListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all config variables for an app",
	Aliases: []string{"ls"},
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

var configUnsetCmd = &cobra.Command{
	Use:   "unset KEY",
	Short: "Remove a config variable",
	Args:  cobra.ExactArgs(1),
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
		if err := c.UnsetConfig(context.Background(), appID, args[0]); err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(map[string]string{"status": "removed", "key": args[0]})
		}

		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Config %q removed from app %s", args[0], appID),
		))
		return nil
	},
}

func init() {
	configSetCmd.Flags().String("app", "", "App name or ID (required)")
	configSetCmd.Flags().Bool("secret", false, "Encrypt value at rest")

	configListCmd.Flags().String("app", "", "App name or ID (required)")

	configUnsetCmd.Flags().String("app", "", "App name or ID (required)")

	configCmd.AddCommand(configSetCmd, configListCmd, configUnsetCmd)
	rootCmd.AddCommand(configCmd)
}
