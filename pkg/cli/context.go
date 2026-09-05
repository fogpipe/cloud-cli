package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Show the resolved CLI context (org, project, api-url, identity)",
	Long: `Print the context the CLI resolves for commands: current organization,
project, API URL, and logged-in identity.

Values follow the usual precedence — an explicit --org/--project flag wins over
the config file (which FPCLOUD_CONFIG_DIR, or FPCLOUD_STATE_DIR for the whole
state dir, may scope to a per-directory location), which wins over the built-in
default.

  fpcloud context                     # human-readable summary
  fpcloud context -o json             # machine-readable
  fpcloud context --format '{org}/{project}'   # terse, for shell prompts

The --format template understands {org}, {project}, {api_url}, and {identity}.
Identity is only looked up (a network call) when the default/json output is used
or {identity} appears in --format, so prompt use stays fast.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		org := rootCmd.Flag("org").Value.String()
		project := rootCmd.Flag("project").Value.String()
		apiURL := resolveAPIURL()

		if format, _ := cmd.Flags().GetString("format"); format != "" {
			out := format
			out = strings.ReplaceAll(out, "{org}", org)
			out = strings.ReplaceAll(out, "{project}", project)
			out = strings.ReplaceAll(out, "{api_url}", apiURL)
			if strings.Contains(out, "{identity}") {
				out = strings.ReplaceAll(out, "{identity}", currentIdentity())
			}
			fmt.Println(out)
			return nil
		}

		identity := currentIdentity()
		// Whether the identity can see the org the context names. A context
		// that names an org the identity cannot reach renders like a working
		// one and fails on every command; saying so here is the difference.
		access := "unknown"
		if identity != "" && org != "" {
			access = "no"
			if orgs, err := getClient().ListOrgs(context.Background()); err == nil && matchOrg(orgs, org) != nil {
				access = "yes"
			}
		}

		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(map[string]string{
				"org":        org,
				"project":    project,
				"api_url":    apiURL,
				"identity":   identity,
				"org_access": access,
			})
		}

		orgShown := orDefault(org, "(unset)")
		if access == "no" {
			orgShown = org + " " + mutedStyle.Render("("+identity+" is not a member; run fpcloud switch)")
		}
		fmt.Println(renderInfoBox("Context", [][]string{
			{"Organization", orgShown},
			{"Project", orDefault(project, "(unset)")},
			{"API URL", apiURL},
			{"Identity", orDefault(identity, "(unauthenticated)")},
		}))
		return nil
	},
}

// currentIdentity returns the logged-in user's email, or "" if the CLI is
// unauthenticated or the control plane is unreachable. It never errors — the
// resolved context is still useful without a live identity.
func currentIdentity() string {
	me, err := getClient().GetMe(context.Background())
	if err != nil || me.User == nil {
		return ""
	}
	return me.User.Email
}

// orDefault returns s, or def when s is empty (with muted styling for display).
func orDefault(s, def string) string {
	if s == "" {
		return mutedStyle.Render(def)
	}
	return s
}

func init() {
	contextCmd.Flags().String("format", "", "Terse template output, e.g. '{org}/{project}' (fields: {org} {project} {api_url} {identity})")
	rootCmd.AddCommand(contextCmd)
}
