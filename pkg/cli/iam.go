package cli

import (
	"context"
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
		fmt.Println(mutedStyle.Render("It holds nothing yet. Grant it a role with:"))
		fmt.Println(mutedStyle.Render("  fpcloud iam set --member serviceAccount:" + sa.Email + " --role viewer"))
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

		headers := []string{"ID", "NAME", "EMAIL", "APP", "STATUS", "CREATED"}
		var rows [][]string
		for _, sa := range accounts {
			app := sa.App
			if app == "" {
				app = mutedStyle.Render("-")
			}
			rows = append(rows, []string{
				sa.ID,
				sa.Name,
				sa.Email,
				app,
				renderStatus(sa.Status),
				sa.CreatedAt.Format("2006-01-02 15:04"),
			})
		}
		render(headers, rows, accounts)
		return nil
	},
}

// A machine identity the ORGANIZATION holds, rather than one of its projects
// (fogpipe/cloud-workspace#778). For a credential that outlives every project —
// a CI suite that creates and destroys its own, a tofu root — which had no home
// in the schema and was written as a user row instead, so the identity
// provisioning task mailed CI an invitation (#741).
var saCreateOrgCmd = &cobra.Command{
	Use:   "create-org <name>",
	Short: "Create a service account the organization holds, not a project",
	Long: "Mint a machine identity scoped to the organization itself, for a credential\n" +
		"that outlives any single project. Requires org administrate. Its address\n" +
		"carries the org and no project: <name>@<org>.<platform domain>.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		displayName, _ := cmd.Flags().GetString("display-name")

		c := getClient()
		var sa *client.ServiceAccount
		withSpinner("Creating service account...", func() {
			sa, err = c.CreateOrgServiceAccount(cmd.Context(), orgID, client.CreateServiceAccountRequest{
				Name:        args[0],
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
			{"Scope", "organization"},
			{"Status", renderStatus(sa.Status)},
		}))
		fmt.Println(mutedStyle.Render("It holds nothing yet. Grant it a role with:"))
		fmt.Println(mutedStyle.Render("  fpcloud iam set --member serviceAccount:" + sa.Email + " --role viewer"))
		return nil
	},
}

// The org's OWN identities, not its projects' — which each project lists.
var saListOrgCmd = &cobra.Command{
	Use:   "list-org",
	Short: "List the service accounts the organization holds",
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}

		c := getClient()
		var accounts []*client.ServiceAccount
		withSpinner("Fetching service accounts...", func() {
			accounts, err = c.ListOrgServiceAccounts(cmd.Context(), orgID)
		})
		if err != nil {
			return err
		}

		headers := []string{"ID", "NAME", "EMAIL", "APP", "STATUS", "CREATED"}
		var rows [][]string
		for _, sa := range accounts {
			app := sa.App
			if app == "" {
				app = mutedStyle.Render("-")
			}
			rows = append(rows, []string{
				sa.ID,
				sa.Name,
				sa.Email,
				app,
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

// accessResource is a resource a binding is held on, named on the command
// line as <kind>/<name>: a database, a bucket or a secret in the current
// project. Resolved to its id here, since every access route is by id.
type accessResource struct {
	Kind, Name, ID string
}

func resolveAccessResource(c *client.Client, ref string) (*accessResource, error) {
	kind, name, ok := strings.Cut(ref, "/")
	if !ok || kind == "" || name == "" {
		return nil, fmt.Errorf("--resource names a database, a bucket or a secret as <kind>/<name>, e.g. database/maindb")
	}
	r := &accessResource{Kind: kind, Name: name}
	var err error
	switch kind {
	case "database":
		r.ID, err = resolveDatabaseID(c, name)
	case "bucket":
		r.ID, err = resolveBucketID(c, name)
	case "secret":
		var sec *client.Secret
		if sec, err = resolveSecret(context.Background(), c, name); err == nil {
			r.ID = sec.ID
		}
	default:
		return nil, fmt.Errorf("a binding is held on a database, a bucket or a secret, not on a %s", kind)
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (r *accessResource) set(ctx context.Context, c *client.Client, req client.SetIAMBindingRequest) (*client.IAMBinding, error) {
	switch r.Kind {
	case "database":
		return c.SetDatabaseAccess(ctx, r.ID, req)
	case "bucket":
		return c.SetBucketAccess(ctx, r.ID, req)
	}
	return c.SetSecretAccess(ctx, r.ID, req)
}

func (r *accessResource) list(ctx context.Context, c *client.Client) ([]*client.IAMBinding, error) {
	switch r.Kind {
	case "database":
		return c.ListDatabaseAccess(ctx, r.ID)
	case "bucket":
		return c.ListBucketAccess(ctx, r.ID)
	}
	return c.ListSecretAccess(ctx, r.ID)
}

func (r *accessResource) remove(ctx context.Context, c *client.Client, bindingID string) error {
	switch r.Kind {
	case "database":
		return c.RemoveDatabaseAccess(ctx, r.ID, bindingID)
	case "bucket":
		return c.RemoveBucketAccess(ctx, r.ID, bindingID)
	}
	return c.RemoveSecretAccess(ctx, r.ID, bindingID)
}

// accessRows renders bindings the way `iam list` and every `get` do.
func accessRows(bindings []*client.IAMBinding) string {
	if len(bindings) == 0 {
		return mutedStyle.Render("-")
	}
	parts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		parts = append(parts, b.MemberType+":"+b.Member+" ("+b.Role+")")
	}
	return strings.Join(parts, ", ")
}

var iamSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set an IAM binding on the project, or hand a resource to a member",
	Long: `Grant a role on the current project, or with --resource hand one of its
resources to a member: database.user on a database, bucket.user on a bucket,
secret.reader on a secret. ` + "`fpcloud iam roles`" + ` lists every role, what
it carries and where it is held. A member may hold several roles on one
target; granting adds one and never replaces another. Granting, a role or a
resource alike, needs iam.set on the project and everything the role carries;
a grant never exceeds the granter. An app's own identity grants nothing.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}

		role, _ := cmd.Flags().GetString("role")
		member, _ := cmd.Flags().GetString("member")
		resource, _ := cmd.Flags().GetString("resource")

		if role == "" {
			if resource == "" {
				return fmt.Errorf("--role is required; `fpcloud iam roles` lists them")
			}
			return fmt.Errorf("--role is required (database.user, bucket.user or secret.reader)")
		}
		if member == "" {
			return fmt.Errorf("--member is required (user:<email> or serviceAccount:<email>)")
		}

		parts := strings.SplitN(member, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("member must be in format type:email (e.g. user:a@b.com or serviceAccount:sa@proj.cloud.fogpipe.com)")
		}

		c := getClient()
		req := client.SetIAMBindingRequest{Role: role, MemberType: parts[0], Member: parts[1]}
		var binding *client.IAMBinding
		if resource != "" {
			r, err := resolveAccessResource(c, resource)
			if err != nil {
				return err
			}
			withSpinner("Handing "+r.Kind+" "+r.Name+" over...", func() {
				binding, err = r.set(cmd.Context(), c, req)
			})
			if err != nil {
				return err
			}
			fmt.Println(renderInfoBox("Access Granted", [][]string{
				{"Binding ID", binding.ID},
				{"Resource", r.Kind + "/" + r.Name},
				{"Role", binding.Role},
				{"Member", binding.MemberType + ":" + binding.Member},
			}))
			return nil
		}
		withSpinner("Setting IAM binding...", func() {
			binding, err = c.SetIAMBinding(cmd.Context(), projectID, req)
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
	Short: "List everyone who can reach a project, org roles included",
	Long: `List everyone who can reach a project: the roles granted on it, and the
organization's, which every project inherits. SCOPE says which one granted a
role. An organization role is removed with ` + "`fpcloud org remove`" + `.
With --resource, list who is handed one of the project's resources instead.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}

		c := getClient()
		var bindings []*client.IAMBinding
		if resource, _ := cmd.Flags().GetString("resource"); resource != "" {
			r, err := resolveAccessResource(c, resource)
			if err != nil {
				return err
			}
			withSpinner("Fetching who is handed "+r.Kind+" "+r.Name+"...", func() {
				bindings, err = r.list(cmd.Context(), c)
			})
			if err != nil {
				return err
			}
		} else {
			withSpinner("Fetching IAM bindings...", func() {
				bindings, err = c.ListIAMBindings(cmd.Context(), projectID)
			})
			if err != nil {
				return err
			}
		}

		headers := []string{"ID", "ROLE", "MEMBER TYPE", "MEMBER", "SCOPE", "CREATED"}
		var rows [][]string
		for _, b := range bindings {
			rows = append(rows, []string{
				b.ID,
				b.Role,
				b.MemberType,
				b.Member,
				bindingScope(b),
				b.CreatedAt,
			})
		}
		render(headers, rows, bindings)
		return nil
	},
}

var iamRolesCmd = &cobra.Command{
	Use:   "roles [role]",
	Short: "List the roles a binding may name, or one role's permissions",
	Long: `The platform's role catalogue: a role is a named set of permissions, and a
binding holds one role for a member on an organization, a project, or a
resource in a project. The three scope roles are derived from each
permission's level (viewer reads, editor also writes, owner also administers
and reads the bill); the others list exactly what they carry. Name a role to
see its permissions.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		var catalog *client.RoleCatalog
		var err error
		withSpinner("Fetching the role catalogue...", func() {
			catalog, err = c.ListRoles(cmd.Context())
		})
		if err != nil {
			return err
		}
		if len(args) == 1 {
			for _, r := range catalog.Roles {
				if r.Name != args[0] {
					continue
				}
				what := map[string]string{}
				for _, p := range catalog.Permissions {
					what[p.Name] = p.What
				}
				pairs := [][]string{{"Role", r.Name}, {"Held on", strings.Join(r.HoldableOn, ", ")}, {"Description", r.Description}}
				for _, p := range r.Permissions {
					pairs = append(pairs, []string{p, what[p]})
				}
				if isStructured(rootCmd.Flag("output").Value.String()) {
					return renderData(r)
				}
				fmt.Println(renderInfoBox("Role "+r.Name, pairs))
				return nil
			}
			return fmt.Errorf("%q is not a role; `fpcloud iam roles` lists them", args[0])
		}
		headers := []string{"ROLE", "HELD ON", "PERMISSIONS", "DESCRIPTION"}
		var rows [][]string
		for _, r := range catalog.Roles {
			rows = append(rows, []string{r.Name, strings.Join(r.HoldableOn, ", "), fmt.Sprint(len(r.Permissions)), r.Description})
		}
		render(headers, rows, catalog)
		return nil
	},
}

var iamRemoveCmd = &cobra.Command{
	Use:   "remove <binding-id>",
	Short: "Remove an IAM binding, or with --resource a grant on a resource",
	Long: `Remove a role granted on the project, or with --resource a grant held on
one of its resources. A grant an app still mounts is refused until the app
stops mounting it.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}

		c := getClient()
		if resource, _ := cmd.Flags().GetString("resource"); resource != "" {
			r, err := resolveAccessResource(c, resource)
			if err != nil {
				return err
			}
			withSpinner("Revoking...", func() {
				err = r.remove(cmd.Context(), c, args[0])
			})
			if err != nil {
				return err
			}
			fmt.Println(successBox.Render("Access revoked."))
			return nil
		}
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

var saGetCmd = &cobra.Command{
	Use:   "get <id|email>",
	Short: "Show a service account and everything it holds",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveServiceAccountID(c, args[0])
		if err != nil {
			return err
		}
		var sa *client.ServiceAccount
		withSpinner("Fetching service account...", func() {
			sa, err = c.GetServiceAccount(cmd.Context(), id)
		})
		if err != nil {
			return err
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(sa)
		}
		app := sa.App
		if app == "" {
			app = mutedStyle.Render("-")
		}
		fmt.Println(renderInfoBox("Service Account", [][]string{
			{"ID", mutedStyle.Render(sa.ID)},
			{"Name", sa.Name},
			{"Email", sa.Email},
			{"App", app},
			{"Status", renderStatus(sa.Status)},
		}))
		if len(sa.Bindings) == 0 {
			fmt.Println()
			fmt.Println(mutedStyle.Render("  Holds nothing. Grant a role with: fpcloud iam set --member serviceAccount:" + sa.Email + " --role <role>"))
			return nil
		}
		fmt.Println()
		headers := []string{"ID", "SCOPE", "ROLE", "CREATED"}
		var rows [][]string
		for _, b := range sa.Bindings {
			rows = append(rows, []string{b.ID, bindingScope(b), b.Role, b.CreatedAt})
		}
		render(headers, rows, sa.Bindings)
		return nil
	},
}

// resolveServiceAccountID accepts an id or an email; an email is looked up
// among the current project's accounts.
func resolveServiceAccountID(c *client.Client, ref string) (string, error) {
	if !strings.Contains(ref, "@") {
		return ref, nil
	}
	projectID, err := requireProject()
	if err != nil {
		return "", err
	}
	accounts, err := c.ListServiceAccounts(context.Background(), projectID)
	if err != nil {
		return "", err
	}
	for _, sa := range accounts {
		if sa.Email == ref {
			return sa.ID, nil
		}
	}
	return "", fmt.Errorf("no service account %s in this project", ref)
}

func init() {
	// Service account commands
	saCreateCmd.Flags().String("display-name", "", "Display name for the service account")
	saUpdateCmd.Flags().String("display-name", "", "New display name")
	saCmd.AddCommand(saCreateCmd)
	saCreateOrgCmd.Flags().String("display-name", "", "Human-readable name")
	saCmd.AddCommand(saCreateOrgCmd)
	saCmd.AddCommand(saListOrgCmd)
	saCmd.AddCommand(saUpdateCmd)
	saCmd.AddCommand(saGetCmd)
	saCmd.AddCommand(saListCmd)
	saCmd.AddCommand(saDeleteCmd)
	saKeysCmd.AddCommand(saKeysCreateCmd, saKeysListCmd, saKeysDeleteCmd)
	saCmd.AddCommand(saKeysCmd)
	rootCmd.AddCommand(saCmd)

	// IAM commands
	iamSetCmd.Flags().String("role", "", "Role to grant; `fpcloud iam roles` lists them")
	iamSetCmd.Flags().String("member", "", "Member in format type:email (user:a@b.com or serviceAccount:sa@proj.cloud.fogpipe.com)")
	iamSetCmd.Flags().String("resource", "", "Hand a resource to the member instead of granting a project role: database/<name>, bucket/<name>, secret/<name> or secret/org/<name>")
	iamListCmd.Flags().String("resource", "", "List who is handed a resource: database/<name>, bucket/<name>, secret/<name> or secret/org/<name>")
	iamRemoveCmd.Flags().String("resource", "", "The resource the binding is held on: database/<name>, bucket/<name>, secret/<name> or secret/org/<name>")
	iamCmd.AddCommand(iamSetCmd)
	iamCmd.AddCommand(iamListCmd)
	iamCmd.AddCommand(iamRolesCmd)
	iamCmd.AddCommand(iamRemoveCmd)
	rootCmd.AddCommand(iamCmd)
}

// bindingScope names what a binding is held on: the kind and the resource's
// name, which the server reads with the row, falling back to the id for a
// resource that is gone.
func bindingScope(b *client.IAMBinding) string {
	if b.ResourceName == "" {
		return b.ResourceType + "/" + b.ResourceID
	}
	return b.ResourceType + "/" + b.ResourceName
}
