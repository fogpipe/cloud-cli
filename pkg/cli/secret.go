package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// orgSecretPrefix marks an organization secret wherever a secret is named: in
// an env reference, on this command's verbs, in `iam set --resource`.
const orgSecretPrefix = "org/"

// secretCmd is the secret store (ADR-243, ADR-287): named values in a project
// or in the organization, reaching an app through a variable in its env.
var secretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Manage secrets, referenced from an app's env",
	Long: `A secret is a named value in the project, or in the organization. An app
reaches it through a variable that references it, as the variable's value or
as a file the variable names (` + "`app env set --from-secret`" + `). Its value
is read back only by asking for it, and every such read is audited.

  fpcloud secret create stripe-key sk_live_...
  fpcloud app env set web STRIPE_KEY --from-secret stripe-key
  fpcloud secret list
  fpcloud secret access stripe-key

A database's owner credential is a secret too, <db>-owner, created with the
database, so referencing it is how an app is handed the database:

  fpcloud app env set web DATABASE_URL --from-secret mydb-owner

A bucket's key is one as well, <bucket>-key, an AWS config file holding the
key, region and endpoint, read by any AWS SDK through AWS_CONFIG_FILE:

  fpcloud app env set web AWS_CONFIG_FILE --from-secret media-key --path /secrets/s3

An organization secret is written once and referenced by an app in any of the
organization's projects; it is named org/<name> everywhere:

  fpcloud secret create org/stripe-key sk_live_...
  fpcloud app env set web STRIPE_KEY --from-secret org/stripe-key
  fpcloud secret list --org`,
}

var secretCreateCmd = &cobra.Command{
	Use:   "create <name> <value>",
	Short: "Create a secret; org/<name> creates it in the organization",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, value := args[0], args[1]
		c := getClient()
		var sec *client.Secret
		if orgName, isOrg := strings.CutPrefix(name, orgSecretPrefix); isOrg {
			orgID, err := resolveOrgID(cmd)
			if err != nil {
				return err
			}
			if sec, err = c.CreateOrgSecret(context.Background(), orgID, orgName, value); err != nil {
				return err
			}
		} else {
			projectID, err := requireProject()
			if err != nil {
				return err
			}
			if sec, err = c.CreateProjectSecret(context.Background(), projectID, name, value); err != nil {
				return err
			}
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(sec)
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Secret %q created. Reference it with `fpcloud app env set <app> KEY --from-secret %s`", secretDisplayName(sec), secretDisplayName(sec)),
		))
		return nil
	},
}

var secretListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List the project's secrets, or the organization's with --org (names, never values)",
	Aliases: []string{"ls"},
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		org, _ := cmd.Flags().GetBool("org")
		var secrets []*client.Secret
		if org {
			orgID, err := resolveOrgID(cmd)
			if err != nil {
				return err
			}
			if secrets, err = c.ListOrgSecrets(context.Background(), orgID); err != nil {
				return err
			}
		} else {
			projectID, err := requireProject()
			if err != nil {
				return err
			}
			if secrets, err = c.ListProjectSecrets(context.Background(), projectID); err != nil {
				return err
			}
		}
		rows := make([][]string, len(secrets))
		for i, sec := range secrets {
			rows[i] = []string{secretDisplayName(sec), secretOwner(sec), secretUsers(sec)}
		}
		render([]string{"NAME", "MANAGED BY", "USED BY"}, rows, secrets)
		return nil
	},
}

var secretGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show a secret: who manages it, who references it, who is handed it; --reveal adds its value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		found, err := resolveSecret(cmd.Context(), c, args[0])
		if err != nil {
			return err
		}
		sec, err := c.GetSecret(context.Background(), found.ID)
		if err != nil {
			return err
		}
		reveal, _ := cmd.Flags().GetBool("reveal")
		var value *client.SecretValue
		if reveal {
			if value, err = c.AccessSecret(context.Background(), sec.ID); err != nil {
				return err
			}
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			if value != nil {
				return renderData(revealedSecret{Secret: sec, Value: value.Value})
			}
			return renderData(sec)
		}
		rows := [][]string{
			{"ID", mutedStyle.Render(sec.ID)},
			{"Name", secretDisplayName(sec)},
			{"Scope", sec.Scope},
			{"Managed by", secretOwner(sec)},
			{"Used by", secretUsers(sec)},
			{"Access", accessRows(sec.Access)},
			{"Updated", sec.UpdatedAt.Format("2006-01-02 15:04")},
		}
		if value != nil {
			rows = append(rows, []string{"Value", value.Value})
		}
		fmt.Println(renderInfoBox("Secret", rows))
		return nil
	},
}

// revealedSecret is a secret's metadata with the value `--reveal` asked for.
type revealedSecret struct {
	*client.Secret
	Value string `json:"value"`
}

var secretAccessCmd = &cobra.Command{
	Use:   "access <name>",
	Short: "Print a secret's value, and nothing else",
	Long: `Print a secret's value exactly as it is held, with nothing around it, so
$(fpcloud secret access stripe-key) is the value. Allowed to whoever may hand
the secret to an app, or holds it; every read is audited.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		sec, err := resolveSecret(cmd.Context(), c, args[0])
		if err != nil {
			return err
		}
		value, err := c.AccessSecret(context.Background(), sec.ID)
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(value)
		}
		fmt.Print(value.Value)
		return nil
	},
}

var secretUpdateCmd = &cobra.Command{
	Use:   "update <name> <value>",
	Short: "Replace a secret's value and roll the apps referencing it",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, value := args[0], args[1]
		c := getClient()
		sec, err := resolveSecret(cmd.Context(), c, name)
		if err != nil {
			return err
		}
		updated, err := c.UpdateSecret(context.Background(), sec.ID, value)
		if err != nil {
			return err
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(updated)
		}
		note := ""
		if len(updated.UsedBy) > 0 {
			note = fmt.Sprintf("; %s rolled onto it", strings.Join(updated.UsedBy, ", "))
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Secret %q updated%s", secretDisplayName(updated), note),
		))
		return nil
	},
}

var secretDeleteCmd = &cobra.Command{
	Use:     "delete <name>",
	Short:   "Delete a secret nothing references",
	Aliases: []string{"rm"},
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		sec, err := resolveSecret(cmd.Context(), c, args[0])
		if err != nil {
			return err
		}
		if err := c.DeleteSecret(context.Background(), sec.ID); err != nil {
			return err
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(map[string]string{"status": "deleted", "name": secretDisplayName(sec)})
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Secret %q deleted", secretDisplayName(sec)),
		))
		return nil
	},
}

// resolveSecret finds a secret by name: in the current project, or in the
// organization when the name carries the org/ prefix. An id is accepted for
// either.
func resolveSecret(ctx context.Context, c *client.Client, name string) (*client.Secret, error) {
	if orgName, isOrg := strings.CutPrefix(name, orgSecretPrefix); isOrg {
		orgID, err := resolveCurrentOrgID(ctx)
		if err != nil {
			return nil, err
		}
		secrets, err := c.ListOrgSecrets(ctx, orgID)
		if err != nil {
			return nil, err
		}
		for _, sec := range secrets {
			if sec.Name == orgName || sec.ID == orgName {
				return sec, nil
			}
		}
		return nil, fmt.Errorf("no secret %q in this organization", orgName)
	}
	projectID, err := requireProject()
	if err != nil {
		return nil, err
	}
	secrets, err := c.ListProjectSecrets(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, sec := range secrets {
		if sec.Name == name || sec.ID == name {
			return sec, nil
		}
	}
	return nil, fmt.Errorf("no secret %q in this project", name)
}

// secretDisplayName is the name a secret is referenced by: bare in its
// project, org/<name> in the organization.
func secretDisplayName(sec *client.Secret) string {
	if sec.Scope == "organization" {
		return orgSecretPrefix + sec.Name
	}
	return sec.Name
}

// secretOwner is who manages the secret and which value of theirs it holds
// (ADR-296): "database (password)", "bucket (access_key_id)".
func secretOwner(sec *client.Secret) string {
	var owner string
	switch {
	case sec.OwnerDatabaseID != "":
		owner = "database"
	case sec.OwnerBucketID != "":
		owner = "bucket"
	case sec.OwnerAppID != "":
		owner = "app"
	default:
		return mutedStyle.Render("-")
	}
	if sec.Part != "" {
		return owner + " (" + sec.Part + ")"
	}
	return owner
}

// ownedSecretsRow renders a resource's owned secrets, part by part, in a
// fixed order so two reads print alike: "url: maindb-owner, password:
// maindb-owner-password".
func ownedSecretsRow(secrets map[string]string) string {
	if len(secrets) == 0 {
		return ""
	}
	order := []string{"url", "password", "access_key_id", "secret_access_key"}
	var parts []string
	seen := map[string]bool{}
	for _, part := range order {
		if name, ok := secrets[part]; ok {
			parts = append(parts, part+": "+name)
			seen[part] = true
		}
	}
	var rest []string
	for part := range secrets {
		if !seen[part] {
			rest = append(rest, part)
		}
	}
	sort.Strings(rest)
	for _, part := range rest {
		parts = append(parts, part+": "+secrets[part])
	}
	return strings.Join(parts, ", ")
}

func secretUsers(sec *client.Secret) string {
	if len(sec.UsedBy) == 0 {
		return mutedStyle.Render("-")
	}
	return strings.Join(sec.UsedBy, ", ")
}

func init() {
	secretListCmd.Flags().Bool("org", false, "List the organization's secrets instead of the project's")
	secretGetCmd.Flags().Bool("reveal", false, "Include the secret's value (audited)")
	secretCmd.AddCommand(secretCreateCmd, secretGetCmd, secretAccessCmd, secretListCmd, secretUpdateCmd, secretDeleteCmd)
	rootCmd.AddCommand(secretCmd)
}
