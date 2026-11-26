package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

var signupCmd = &cobra.Command{
	Use:   "signup",
	Short: "Create an organization with a signup code",
	Long: `Create an organization with a signup code you were given.

  fpcloud signup --code "12345 67890" --email you@example.com --org-name "Acme"

The organization comes with the credit the code carries. An email follows to
the address you give, within a few minutes: set up your sign-in from it, then
run fpcloud login. Needs no login of its own.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		code, _ := cmd.Flags().GetString("code")
		email, _ := cmd.Flags().GetString("email")
		name, _ := cmd.Flags().GetString("org-name")
		c := newClient(resolveAPIURL(), "")
		if err := c.Signup(cmd.Context(), client.SignupRequest{Code: code, Email: email, OrgName: name}); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Created %s. We've sent an email to %s; it can take a few minutes.\n", name, email)
		return nil
	},
}

func init() {
	signupCmd.Flags().String("code", "", "The signup code, ten digits (spaces and dashes are ignored)")
	signupCmd.Flags().String("email", "", "Your email address; the organization is yours once you sign in with it")
	signupCmd.Flags().String("org-name", "", "Your organization's name")
	for _, f := range []string{"code", "email", "org-name"} {
		_ = signupCmd.MarkFlagRequired(f)
	}
	rootCmd.AddCommand(signupCmd)
}
