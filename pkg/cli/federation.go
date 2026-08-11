package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// githubActionsIssuer is the OIDC issuer for `federation github add`.
const githubActionsIssuer = "https://token.actions.githubusercontent.com"

var federationCmd = &cobra.Command{
	Use:     "federation",
	Aliases: []string{"fed"},
	Short:   "Federate external OIDC identities to service accounts (no stored keys)",
}

// federationGithubCmd groups GitHub Actions federation. The provider is always
// explicit so future issuers slot in as siblings (e.g. `federation gitlab add`).
var federationGithubCmd = &cobra.Command{
	Use:   "github",
	Short: "Trust GitHub repos/refs to assume a service account (GitHub Actions OIDC)",
}

var federationGithubAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Trust a GitHub repo/ref to assume a service account",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}

		issuer := githubActionsIssuer
		audience, _ := cmd.Flags().GetString("audience")
		subject, _ := cmd.Flags().GetString("subject")
		repo, _ := cmd.Flags().GetString("repo")
		ref, _ := cmd.Flags().GetString("ref")
		sa, _ := cmd.Flags().GetString("service-account")
		ttl, _ := cmd.Flags().GetInt("ttl")

		// Convenience: build a GitHub subject from --repo/--ref if --subject omitted.
		if subject == "" && repo != "" {
			if ref != "" {
				subject = fmt.Sprintf("repo:%s:ref:%s", repo, ref)
			} else {
				subject = fmt.Sprintf("repo:%s:*", repo)
			}
		}
		if subject == "" {
			return fmt.Errorf("provide --subject, or --repo (optionally with --ref)")
		}
		if sa == "" {
			return fmt.Errorf("--service-account is required")
		}

		c := getClient()
		var binding *client.TrustBinding
		withSpinner("Creating trust binding...", func() {
			binding, err = c.CreateTrustBinding(cmd.Context(), projectID, client.CreateTrustBindingRequest{
				Issuer:          issuer,
				Audience:        audience,
				SubjectPattern:  subject,
				ServiceAccount:  sa,
				TokenTTLSeconds: ttl,
			})
		})
		if err != nil {
			return err
		}

		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(binding)
		}
		fmt.Println(renderInfoBox("Trust binding created", [][]string{
			{"ID", mutedStyle.Render(binding.ID)},
			{"Issuer", binding.Issuer},
			{"Audience", binding.Audience},
			{"Subject", binding.SubjectPattern},
			{"Token TTL", fmt.Sprintf("%ds", binding.TokenTTLSeconds)},
		}))
		return nil
	},
}

var federationListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List OIDC federation trust bindings for a project",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}

		c := getClient()
		var bindings []*client.TrustBinding
		withSpinner("Fetching trust bindings...", func() {
			bindings, err = c.ListTrustBindings(cmd.Context(), projectID)
		})
		if err != nil {
			return err
		}

		headers := []string{"ID", "ISSUER", "AUDIENCE", "SUBJECT", "TTL"}
		var rows [][]string
		for _, b := range bindings {
			rows = append(rows, []string{
				b.ID,
				shortIssuer(b.Issuer),
				b.Audience,
				b.SubjectPattern,
				fmt.Sprintf("%ds", b.TokenTTLSeconds),
			})
		}
		render(headers, rows, bindings)
		return nil
	},
}

var federationRemoveCmd = &cobra.Command{
	Use:     "remove <binding-id>",
	Aliases: []string{"rm"},
	Short:   "Remove an OIDC federation trust binding",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}

		c := getClient()
		withSpinner("Removing trust binding...", func() {
			err = c.DeleteTrustBinding(cmd.Context(), projectID, args[0])
		})
		if err != nil {
			return err
		}
		fmt.Println(successBox.Render("Trust binding removed."))
		return nil
	},
}

// shortIssuer trims the well-known GitHub issuer to a compact label for tables.
func shortIssuer(issuer string) string {
	if issuer == githubActionsIssuer {
		return "github-actions"
	}
	return issuer
}

func init() {
	federationGithubAddCmd.Flags().String("audience", "fpcloud", "OIDC audience the token must carry")
	federationGithubAddCmd.Flags().String("subject", "", "Subject pattern to match (e.g. repo:owner/name:ref:refs/tags/*)")
	federationGithubAddCmd.Flags().String("repo", "", "GitHub repo owner/name (shortcut to build --subject)")
	federationGithubAddCmd.Flags().String("ref", "", "Git ref to allow with --repo (e.g. refs/tags/*); omit to allow any ref")
	federationGithubAddCmd.Flags().String("service-account", "", "Service account (email or id) the repo may assume")
	federationGithubAddCmd.Flags().Int("ttl", 900, "Lifetime of minted tokens, in seconds")

	federationGithubCmd.AddCommand(federationGithubAddCmd)
	federationCmd.AddCommand(federationGithubCmd, federationListCmd, federationRemoveCmd)
	rootCmd.AddCommand(federationCmd)
}
