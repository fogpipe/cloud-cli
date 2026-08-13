package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication",
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in with Google (gcloud-style), or --api-key for a static key",
	Long: "Log in to fpcloud.\n\n" +
		"With no flags this runs the Google browser login (like `gcloud auth login`);\n" +
		"that identity authenticates both the API and kubectl, so no separate key is\n" +
		"needed. Pass --api-key for a static key (CI / service accounts).",
	RunE: func(cmd *cobra.Command, args []string) error {
		apiKey := cmd.Flag("api-key").Value.String()
		if apiKey == "" {
			// gcloud-style browser login; the OIDC token also authenticates the API.
			return loginCmd.RunE(loginCmd, args)
		}

		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		// Verify BEFORE writing. Saving first meant a rejected key had already
		// replaced a working one by the time it was checked, and the failure was
		// reported as "saved (server may be unreachable)" with exit 0 — so the user
		// was told their credentials were fine, had lost the ones that were, and
		// was pointed at the network instead of the key (#568).
		apiURL := cmd.Flag("api-url").Value.String()
		me, err := newClient(apiURL, apiKey).GetMe(context.Background())
		if err != nil {
			// A rejected key and an unreachable server are different answers and
			// need different next steps; the old code folded them into one.
			var apiErr *client.APIError
			if errors.As(err, &apiErr) {
				return fmt.Errorf("the API rejected this key (%s) — nothing was changed, your existing credentials are untouched", apiErr.Error())
			}
			return fmt.Errorf("could not reach %s to verify the key (%w) — nothing was changed; retry when the API is reachable", apiURL, err)
		}

		cfg.APIKey = apiKey
		if err := saveConfig(cfg); err != nil {
			return err
		}

		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Logged in as %s (%s)", me.User.Name, me.User.Email),
		))
		return nil
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current authentication status",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Resolve the credential the same way getClient() does: a static API key
		// if set, otherwise the Google OIDC token from `fpcloud auth login`.
		apiURL := cmd.Flag("api-url").Value.String()
		cred := cmd.Flag("api-key").Value.String()
		credType := "API key"
		if cred == "" {
			if token, err := currentIDToken(); err == nil {
				cred = token
				credType = "Google login"
			}
		}

		wide, _ := cmd.Flags().GetBool("wide")
		jsonOut := isStructured(rootCmd.Flag("output").Value.String())

		if cred == "" {
			if jsonOut {
				return renderData(map[string]any{"authenticated": false})
			}
			fmt.Println(mutedStyle.Render("Not authenticated. Run `fpcloud auth login`."))
			return nil
		}

		c := newClient(apiURL, cred)
		me, err := c.GetMe(context.Background())
		if err != nil {
			if jsonOut {
				return renderData(map[string]any{
					"authenticated": false,
					"credential":    credType,
					"error":         "server unreachable: " + apiURL,
				})
			}
			if wide {
				fmt.Println(renderInfoBox("Authentication", [][]string{
					{"Credential", credType},
					{"Status", mutedStyle.Render("server unreachable: " + apiURL)},
				}))
				return nil
			}
			fmt.Printf("authenticated: false\ncredential: %s\nerror: server unreachable: %s\n", credType, apiURL)
			return nil
		}

		curOrg := rootCmd.Flag("org").Value.String()
		if curOrg == "" {
			curOrg = mutedStyle.Render("(unset)")
		}
		curProject := rootCmd.Flag("project").Value.String()
		if curProject == "" {
			curProject = mutedStyle.Render("(unset)")
		}

		fields := [][]string{
			{"Email", me.User.Email},
			{"Name", me.User.Name},
			{"Credential", credType},
			{"Organization", me.Organization.DisplayName},
			{"User ID", me.User.ID},
			{"Org ID", me.Organization.ID},
			{"Active org", curOrg},
			{"Active project", curProject},
		}

		if jsonOut {
			return renderData(map[string]any{
				"authenticated":  true,
				"email":          me.User.Email,
				"name":           me.User.Name,
				"credential":     credType,
				"organization":   me.Organization.DisplayName,
				"user_id":        me.User.ID,
				"org_id":         me.Organization.ID,
				"active_org":     rootCmd.Flag("org").Value.String(),
				"active_project": rootCmd.Flag("project").Value.String(),
			})
		}

		if wide {
			fmt.Println(renderInfoBox("Authentication", fields))
			return nil
		}

		// Default: plain, aligned key: value lines — CLI/LLM-friendly, greppable.
		for _, f := range fields {
			fmt.Printf("%-13s %s\n", strings.ToLower(strings.ReplaceAll(f[0], " ", "_"))+":", f[1])
		}
		return nil
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove saved credentials (API key and browser session)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Both credentials, because there are two. Clearing only the API key left
		// the cached Google refresh token in place, and getClient falls through to
		// it — so the session kept working with full access after the user was told
		// their credentials were removed (#568). On a shared machine that is the
		// difference between logging out and believing you have.
		var cleared []string

		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if cfg.APIKey != "" {
			cfg.APIKey = ""
			if err := saveConfig(cfg); err != nil {
				return err
			}
			cleared = append(cleared, "API key")
		}

		if err := os.Remove(tokenCachePath()); err == nil {
			cleared = append(cleared, "browser session")
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("remove cached login token: %w", err)
		}

		if len(cleared) == 0 {
			fmt.Println(mutedStyle.Render("Nothing to remove — no credentials were stored."))
			return nil
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Removed: %s.", strings.Join(cleared, " and ")),
		))
		return nil
	},
}

func init() {
	authStatusCmd.Flags().Bool("wide", false, "Show the detailed boxed view")
	authCmd.AddCommand(authLoginCmd, authStatusCmd, authLogoutCmd, authConfigureDockerCmd)
	rootCmd.AddCommand(authCmd)
}
