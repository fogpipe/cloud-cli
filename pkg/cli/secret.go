package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// secretCmd is the project secret store (#1069): named values that reach an
// app only as mounted files. Distinct from `fpcloud secrets`, the org-wide
// bundle store.
var secretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Manage the project's secrets, mounted into apps as files",
	Long: `A secret is a named value in the project. Its value goes in here and never
comes back out: an app reads it as a file, mounted at a path the app names.

  fpcloud secret create stripe-key sk_live_...
  fpcloud app update web --mount-secret /secrets/stripe=stripe-key
  fpcloud secret list

A database's owner credential is a secret too — <db>-owner, created with the
database — so mounting it is how an app is handed the database:

  fpcloud app update web --mount-secret /secrets/db=mydb-owner

Env stays plain (` + "`fpcloud app env`" + `); org-wide bundles are ` + "`fpcloud secrets`" + `.`,
}

var secretCreateCmd = &cobra.Command{
	Use:   "create <name> <value>",
	Short: "Create a secret",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, value := args[0], args[1]
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		c := getClient()
		sec, err := c.CreateProjectSecret(context.Background(), projectID, name, value)
		if err != nil {
			return err
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(sec)
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Secret %q created — mount it with `fpcloud app update <app> --mount-secret /path=%s`", sec.Name, sec.Name),
		))
		return nil
	},
}

var secretListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List the project's secrets (names, never values)",
	Aliases: []string{"ls"},
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		c := getClient()
		secrets, err := c.ListProjectSecrets(context.Background(), projectID)
		if err != nil {
			return err
		}
		rows := make([][]string, len(secrets))
		for i, sec := range secrets {
			owner := mutedStyle.Render("—")
			if sec.OwnerDatabaseID != "" {
				owner = "database"
			}
			mounted := mutedStyle.Render("—")
			if len(sec.MountedBy) > 0 {
				mounted = strings.Join(sec.MountedBy, ", ")
			}
			rows[i] = []string{sec.Name, owner, mounted}
		}
		render([]string{"NAME", "MANAGED BY", "MOUNTED BY"}, rows, secrets)
		return nil
	},
}

var secretUpdateCmd = &cobra.Command{
	Use:   "update <name> <value>",
	Short: "Replace a secret's value and roll the apps mounting it",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, value := args[0], args[1]
		c := getClient()
		sec, err := resolveSecret(c, name)
		if err != nil {
			return err
		}
		updated, err := c.UpdateProjectSecret(context.Background(), sec.ID, value)
		if err != nil {
			return err
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(updated)
		}
		note := ""
		if len(updated.MountedBy) > 0 {
			note = fmt.Sprintf(" — %s rolled onto it", strings.Join(updated.MountedBy, ", "))
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Secret %q updated%s", updated.Name, note),
		))
		return nil
	},
}

var secretDeleteCmd = &cobra.Command{
	Use:     "delete <name>",
	Short:   "Delete a secret nothing mounts",
	Aliases: []string{"rm"},
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		sec, err := resolveSecret(c, args[0])
		if err != nil {
			return err
		}
		if err := c.DeleteProjectSecret(context.Background(), sec.ID); err != nil {
			return err
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(map[string]string{"status": "deleted", "name": sec.Name})
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Secret %q deleted", sec.Name),
		))
		return nil
	},
}

// resolveSecret finds a project secret by name in the current project.
func resolveSecret(c *client.Client, name string) (*client.ProjectSecret, error) {
	projectID, err := requireProject()
	if err != nil {
		return nil, err
	}
	secrets, err := c.ListProjectSecrets(context.Background(), projectID)
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

func init() {
	secretCmd.AddCommand(secretCreateCmd, secretListCmd, secretUpdateCmd, secretDeleteCmd)
	rootCmd.AddCommand(secretCmd)
}
