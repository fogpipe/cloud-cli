package cli

import (
	"fmt"
	"strings"

	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

// --- Service Account commands ---

var saCmd = &cobra.Command{
	Use:     "sa",
	Aliases: []string{"sas", "service-account", "service-accounts"},
	Short:   "Manage service accounts",
}

var saCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a service account",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}

		name := args[0]
		displayName, _ := cmd.Flags().GetString("display-name")

		c := getClient()
		var sa *client.ServiceAccount
		withSpinner("Creating service account...", func() {
			sa, err = c.CreateServiceAccount(cmd.Context(), projectID, client.CreateServiceAccountRequest{
				Name:        name,
				DisplayName: displayName,
			})
		})
		if err != nil {
			return err
		}

		fmt.Println(renderInfoBox("Service Account Created", [][]string{
			{"ID", sa.ID},
			{"Name", sa.Name},
			{"Email", sa.Email},
			{"Status", renderStatus(sa.Status)},
		}))
		return nil
	},
}

var saListCmd = &cobra.Command{
	Use:   "list",
	Short: "List service accounts",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}

		c := getClient()
		var accounts []*client.ServiceAccount
		withSpinner("Fetching service accounts...", func() {
			accounts, err = c.ListServiceAccounts(cmd.Context(), projectID)
		})
		if err != nil {
			return err
		}

		headers := []string{"ID", "NAME", "EMAIL", "STATUS", "CREATED"}
		var rows [][]string
		for _, sa := range accounts {
			rows = append(rows, []string{
				sa.ID,
				sa.Name,
				sa.Email,
				renderStatus(sa.Status),
				sa.CreatedAt.Format("2006-01-02 15:04"),
			})
		}
		render(headers, rows, accounts)
		return nil
	},
}

var saUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a service account's display name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		displayName, _ := cmd.Flags().GetString("display-name")
		if displayName == "" {
			return fmt.Errorf("--display-name is required")
		}
		c := getClient()
		var sa *client.ServiceAccount
		var err error
		withSpinner("Updating service account...", func() {
			sa, err = c.UpdateServiceAccountDisplayName(cmd.Context(), args[0], displayName)
		})
		if err != nil {
			return err
		}
		fmt.Println(renderInfoBox("Service Account Updated", [][]string{
			{"ID", sa.ID},
			{"Name", sa.Name},
			{"Display Name", sa.DisplayName},
			{"Email", sa.Email},
			{"Status", renderStatus(sa.Status)},
		}))
		return nil
	},
}

var saDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a service account",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		var err error
		withSpinner("Deleting service account...", func() {
			err = c.DeleteServiceAccount(cmd.Context(), args[0])
		})
		if err != nil {
			return err
		}
		fmt.Println(successBox.Render("Service account deleted."))
		return nil
	},
}

var saKeysCmd = &cobra.Command{
	Use:     "keys",
	Aliases: []string{"key"},
	Short:   "Manage service account API keys",
}

var saKeysCreateCmd = &cobra.Command{
	Use:   "create <sa-id>",
	Short: "Create a key for a service account",
	Args:  cobra.ExactArgs(1),
	RunE:  runSAKeyCreate,
}

var saKeysListCmd = &cobra.Command{
	Use:     "list <sa-id>",
	Aliases: []string{"ls"},
	Short:   "List keys for a service account",
	Args:    cobra.ExactArgs(1),
	RunE:    runSAKeyList,
}

var saKeysDeleteCmd = &cobra.Command{
	Use:   "delete <sa-id> <key-id>",
	Short: "Delete a service account key",
	Args:  cobra.ExactArgs(2),
	RunE:  runSAKeyDelete,
}

func runSAKeyCreate(cmd *cobra.Command, args []string) error {
	c := getClient()
	var key *client.ServiceAccountKey
	var err error
	withSpinner("Creating key...", func() {
		key, err = c.CreateServiceAccountKey(cmd.Context(), args[0])
	})
	if err != nil {
		return err
	}

	fmt.Println(renderInfoBox("Service Account Key Created", [][]string{
		{"Key ID", key.ID},
		{"API Key", key.APIKey},
		{"Prefix", key.Prefix},
	}))
	fmt.Println(errorBox.Render("Save this API key now. It will not be shown again."))
	return nil
}

func runSAKeyList(cmd *cobra.Command, args []string) error {
	c := getClient()
	var keys []*client.ServiceAccountKey
	var err error
	withSpinner("Fetching keys...", func() {
		keys, err = c.ListServiceAccountKeys(cmd.Context(), args[0])
	})
	if err != nil {
		return err
	}

	headers := []string{"ID", "PREFIX", "CREATED"}
	var rows [][]string
	for _, k := range keys {
		rows = append(rows, []string{
			k.ID,
			k.Prefix,
			k.CreatedAt,
		})
	}
	render(headers, rows, keys)
	return nil
}

func runSAKeyDelete(cmd *cobra.Command, args []string) error {
	c := getClient()
	var err error
	withSpinner("Deleting key...", func() {
		err = c.DeleteServiceAccountKey(cmd.Context(), args[0], args[1])
	})
	if err != nil {
		return err
	}
	fmt.Println(successBox.Render("Service account key deleted."))
	return nil
}

// --- IAM commands ---

var iamCmd = &cobra.Command{
	Use:   "iam",
	Short: "Manage IAM bindings",
}

var iamSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set an IAM binding on a project",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}

		role, _ := cmd.Flags().GetString("role")
		member, _ := cmd.Flags().GetString("member")

		if role == "" {
			return fmt.Errorf("--role is required (owner, editor, viewer)")
		}
		if member == "" {
			return fmt.Errorf("--member is required (user:<email> or serviceAccount:<email>)")
		}

		parts := strings.SplitN(member, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("member must be in format type:email (e.g. user:a@b.com or serviceAccount:sa@proj.cloud.fogpipe.com)")
		}

		c := getClient()
		var binding *client.IAMBinding
		withSpinner("Setting IAM binding...", func() {
			binding, err = c.SetIAMBinding(cmd.Context(), projectID, client.SetIAMBindingRequest{
				Role:       role,
				MemberType: parts[0],
				Member:     parts[1],
			})
		})
		if err != nil {
			return err
		}

		fmt.Println(renderInfoBox("IAM Binding Set", [][]string{
			{"Binding ID", binding.ID},
			{"Role", binding.Role},
			{"Member", binding.MemberType + ":" + binding.Member},
		}))
		return nil
	},
}

var iamListCmd = &cobra.Command{
	Use:   "list",
	Short: "List IAM bindings for a project",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}

		c := getClient()
		var bindings []*client.IAMBinding
		withSpinner("Fetching IAM bindings...", func() {
			bindings, err = c.ListIAMBindings(cmd.Context(), projectID)
		})
		if err != nil {
			return err
		}

		headers := []string{"ID", "ROLE", "MEMBER TYPE", "MEMBER", "CREATED"}
		var rows [][]string
		for _, b := range bindings {
			rows = append(rows, []string{
				b.ID,
				b.Role,
				b.MemberType,
				b.Member,
				b.CreatedAt,
			})
		}
		render(headers, rows, bindings)
		return nil
	},
}

var iamRemoveCmd = &cobra.Command{
	Use:   "remove <binding-id>",
	Short: "Remove an IAM binding",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}

		c := getClient()
		withSpinner("Removing IAM binding...", func() {
			err = c.RemoveIAMBinding(cmd.Context(), projectID, args[0])
		})
		if err != nil {
			return err
		}
		fmt.Println(successBox.Render("IAM binding removed."))
		return nil
	},
}

func init() {
	// Service account commands
	saCreateCmd.Flags().String("display-name", "", "Display name for the service account")
	saUpdateCmd.Flags().String("display-name", "", "New display name")
	saCmd.AddCommand(saCreateCmd)
	saCmd.AddCommand(saUpdateCmd)
	saCmd.AddCommand(saListCmd)
	saCmd.AddCommand(saDeleteCmd)
	saKeysCmd.AddCommand(saKeysCreateCmd, saKeysListCmd, saKeysDeleteCmd)
	saCmd.AddCommand(saKeysCmd)
	rootCmd.AddCommand(saCmd)

	// IAM commands
	iamSetCmd.Flags().String("role", "", "Role to grant (owner, editor, viewer)")
	iamSetCmd.Flags().String("member", "", "Member in format type:id (e.g. user:abc123)")
	iamCmd.AddCommand(iamSetCmd)
	iamCmd.AddCommand(iamListCmd)
	iamCmd.AddCommand(iamRemoveCmd)
	rootCmd.AddCommand(iamCmd)
}
