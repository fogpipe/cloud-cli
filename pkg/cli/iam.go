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
		fmt.Println(mutedStyle.Render("  fpcloud iam grant --member serviceAccount:" + sa.Email + " --role viewer"))
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
		fmt.Println(mutedStyle.Render("  fpcloud iam grant --member serviceAccount:" + sa.Email + " --role viewer"))
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

// bindingTarget is what a binding is held on, named on the command line with
// --on: "organization", or a database, bucket or secret as <kind>/<name>,
// resolved to its id here since the wire carries ids. Absent, the target is
// the current project.
type bindingTarget struct {
	Org      bool   // the organization itself
	Resource string // <kind>/<id> for a resource, "" for a scope
	Label    string // what to print: "organization", "project" or <kind>/<name>
}

func resolveBindingTarget(c *client.Client, on string) (*bindingTarget, error) {
	if on == "" {
		return &bindingTarget{Label: "project"}, nil
	}
	if on == "organization" {
		return &bindingTarget{Org: true, Label: "organization"}, nil
	}
	kind, name, ok := strings.Cut(on, "/")
	if !ok || kind == "" || name == "" {
		return nil, fmt.Errorf("--on is organization, or a database, bucket or secret as <kind>/<name>, e.g. database/maindb")
	}
	var id string
	var err error
	switch kind {
	case "database":
		id, err = resolveDatabaseID(c, name)
	case "bucket":
		id, err = resolveBucketID(c, name)
	case "secret":
		var sec *client.Secret
		if sec, err = resolveSecret(context.Background(), c, name); err == nil {
			id = sec.ID
			if sec.Scope == "organization" {
				return &bindingTarget{Org: true, Resource: "secret/" + id, Label: "secret/" + name}, nil
			}
		}
	default:
		return nil, fmt.Errorf("a binding is held on a database, a bucket or a secret, not on a %s", kind)
	}
	if err != nil {
		return nil, err
	}
	return &bindingTarget{Resource: kind + "/" + id, Label: on}, nil
}

// parseMember splits user:<email> or serviceAccount:<email>.
func parseMember(member string) (memberType, address string, err error) {
	memberType, address, ok := strings.Cut(member, ":")
	if !ok || memberType == "" || address == "" {
		return "", "", fmt.Errorf("--member is user:<email>, or serviceAccount:<email> or serviceAccount:<name>")
	}
	return memberType, address, nil
}

// resolveMember turns a bare service account name into its address, looked
// up among the target scope's own accounts: the project's, or the
// organization's for an organization target. An address, and a user, pass
// through as given.
func resolveMember(ctx context.Context, c *client.Client, memberType, address string, t *bindingTarget, orgID, projectID string) (string, error) {
	if memberType != "serviceAccount" || strings.Contains(address, "@") {
		return address, nil
	}
	var accounts []*client.ServiceAccount
	var err error
	scope := "this project"
	if t.Org {
		accounts, err = c.ListOrgServiceAccounts(ctx, orgID)
		scope = "this organization"
	} else {
		accounts, err = c.ListServiceAccounts(ctx, projectID)
	}
	if err != nil {
		return "", err
	}
	for _, sa := range accounts {
		if sa.Name == address {
			return sa.Email, nil
		}
	}
	return "", fmt.Errorf("no service account named %q in %s; name one of its own, or give the address (serviceAccount:<email>)", address, scope)
}

// listBindings reads the bindings of the scope a target is in.
func listBindings(ctx context.Context, c *client.Client, t *bindingTarget, orgID, projectID string) ([]*client.IAMBinding, error) {
	if t.Org {
		return c.ListOrgIAMBindings(ctx, orgID)
	}
	return c.ListIAMBindings(ctx, projectID)
}

// scopeIDs resolves the scope a target needs: the organization when the
// target is the organization or one of its secrets, else the current project.
func scopeIDs(cmd *cobra.Command, t *bindingTarget) (orgID, projectID string, err error) {
	if t.Org {
		orgID, err = resolveOrgID(cmd)
		return orgID, "", err
	}
	projectID, err = requireProject()
	return "", projectID, err
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

var iamGrantCmd = &cobra.Command{
	Use:   "grant",
	Short: "Grant a role on the project, the organization, or a resource",
	Long: `Grant a role to a member on the current project, or with --on on the
organization or on one of the project's resources: database.user on a
database, bucket.reader or bucket.user on a bucket, secret.reader on a
secret. ` + "`fpcloud iam roles`" + ` lists every role, what it carries and where it
is held. A member may hold
several roles on one target; granting adds one and never replaces another.
Granting needs iam.set on the scope and everything the role carries; a grant
never exceeds the granter, and an app's own identity grants nothing.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		role, _ := cmd.Flags().GetString("role")
		member, _ := cmd.Flags().GetString("member")
		on, _ := cmd.Flags().GetString("on")
		if role == "" {
			return fmt.Errorf("--role is required; `fpcloud iam roles` lists them")
		}
		memberType, address, err := parseMember(member)
		if err != nil {
			return err
		}
		c := getClient()
		target, err := resolveBindingTarget(c, on)
		if err != nil {
			return err
		}
		orgID, projectID, err := scopeIDs(cmd, target)
		if err != nil {
			return err
		}
		if address, err = resolveMember(cmd.Context(), c, memberType, address, target, orgID, projectID); err != nil {
			return err
		}
		req := client.SetIAMBindingRequest{Role: role, MemberType: memberType, Member: address, Resource: target.Resource}
		var binding *client.IAMBinding
		withSpinner("Granting...", func() {
			if target.Org {
				binding, err = c.SetOrgIAMBinding(cmd.Context(), orgID, req)
			} else {
				binding, err = c.SetIAMBinding(cmd.Context(), projectID, req)
			}
		})
		if err != nil {
			return err
		}
		fmt.Println(renderInfoBox("Granted", [][]string{
			{"Binding ID", binding.ID},
			{"Role", binding.Role},
			{"Member", binding.MemberType + ":" + binding.Member},
			{"On", target.Label},
		}))
		return nil
	},
}

var iamListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every binding that reaches the project, or the organization",
	Long: `List everyone who can reach the current project: the roles granted on it, the
resources in it handed to a member (ON names which), and the organization's
roles, which every project inherits (SCOPE organization). With --on
organization, list the organization's own bindings instead.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		on, _ := cmd.Flags().GetString("on")
		if on != "" && on != "organization" {
			return fmt.Errorf("--on takes organization here; a resource's holders are the rows naming it in the project list")
		}
		c := getClient()
		target, _ := resolveBindingTarget(c, on)
		orgID, projectID, err := scopeIDs(cmd, target)
		if err != nil {
			return err
		}
		var bindings []*client.IAMBinding
		withSpinner("Fetching bindings...", func() {
			bindings, err = listBindings(cmd.Context(), c, target, orgID, projectID)
		})
		if err != nil {
			return err
		}
		headers := []string{"ID", "ROLE", "MEMBER", "ON", "CREATED"}
		var rows [][]string
		for _, b := range bindings {
			rows = append(rows, []string{b.ID, b.Role, b.MemberType + ":" + b.Member, bindingOn(b), b.CreatedAt})
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

var iamRevokeCmd = &cobra.Command{
	Use:   "revoke",
	Short: "Revoke a role from a member on the project, the organization, or a resource",
	Long: `Revoke one binding: the member's role on the current project, or with --on on
the organization or on one of the project's resources. A grant an app still
references in its env is refused until the reference goes. Revoking needs what
granting needs.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		role, _ := cmd.Flags().GetString("role")
		member, _ := cmd.Flags().GetString("member")
		on, _ := cmd.Flags().GetString("on")
		if role == "" {
			return fmt.Errorf("--role is required: the one role to take back")
		}
		memberType, address, err := parseMember(member)
		if err != nil {
			return err
		}
		c := getClient()
		target, err := resolveBindingTarget(c, on)
		if err != nil {
			return err
		}
		orgID, projectID, err := scopeIDs(cmd, target)
		if err != nil {
			return err
		}
		if address, err = resolveMember(cmd.Context(), c, memberType, address, target, orgID, projectID); err != nil {
			return err
		}
		var bindings []*client.IAMBinding
		withSpinner("Finding the binding...", func() {
			bindings, err = listBindings(cmd.Context(), c, target, orgID, projectID)
		})
		if err != nil {
			return err
		}
		var found *client.IAMBinding
		for _, b := range bindings {
			if b.Inherited || b.Role != role || b.MemberType != memberType || !strings.EqualFold(b.Member, address) || b.Resource != target.Resource {
				continue
			}
			found = b
		}
		if found == nil {
			return fmt.Errorf("%s holds no %s on %s", member, role, target.Label)
		}
		withSpinner("Revoking...", func() {
			if target.Org {
				err = c.RemoveOrgIAMBinding(cmd.Context(), orgID, found.ID)
			} else {
				err = c.RemoveIAMBinding(cmd.Context(), projectID, found.ID)
			}
		})
		if err != nil {
			return err
		}
		fmt.Println(successBox.Render("Revoked " + role + " from " + member + " on " + target.Label + "."))
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
			fmt.Println(mutedStyle.Render("  Holds nothing. Grant a role with: fpcloud iam grant --member serviceAccount:" + sa.Email + " --role <role>"))
			return nil
		}
		fmt.Println()
		headers := []string{"ID", "SCOPE", "ROLE", "CREATED"}
		var rows [][]string
		for _, b := range sa.Bindings {
			rows = append(rows, []string{b.ID, bindingOn(b), b.Role, b.CreatedAt})
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
	for _, c := range []*cobra.Command{iamGrantCmd, iamRevokeCmd} {
		c.Flags().String("role", "", "The role; `fpcloud iam roles` lists them")
		c.Flags().String("member", "", "user:<email>, or serviceAccount:<email> or serviceAccount:<name> for one of the scope's own accounts")
		c.Flags().String("on", "", "What the binding is held on: organization, or database/<name>, bucket/<name>, secret/<name>; the current project when absent")
	}
	iamListCmd.Flags().String("on", "", "organization, to list the organization's own bindings instead of the project's")
	iamCmd.AddCommand(iamGrantCmd)
	iamCmd.AddCommand(iamRevokeCmd)
	iamCmd.AddCommand(iamListCmd)
	iamCmd.AddCommand(iamRolesCmd)
	rootCmd.AddCommand(iamCmd)
}

// bindingOn names what a binding is held on: "project" or "organization" for a
// scope's own row, "organization (inherited)" for an organization's row in a
// project's list, and <kind>/<name> for a resource, the id when the name is gone.
func bindingOn(b *client.IAMBinding) string {
	switch {
	case b.Inherited:
		return "organization (inherited)"
	case b.ResourceType == "project" || b.ResourceType == "organization":
		return b.ResourceType
	case b.ResourceName == "":
		return b.ResourceType + "/" + b.ResourceID
	}
	return b.ResourceType + "/" + b.ResourceName
}
