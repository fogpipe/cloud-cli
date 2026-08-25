package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"
)

// Dynamic shell completion: resource names (projects, orgs, apps, …) are
// fetched from the API on TAB, and enum-style flags complete their fixed value
// set. Wired up by registerCompletions, called from main() once the whole
// command tree and all flags exist. Every completer fails open — on any error
// (offline, unauthenticated, no project selected) it returns no suggestions and
// suppresses file completion, so TAB is never noisy or slow.

const noFile = cobra.ShellCompDirectiveNoFileComp

// compCtx bounds every completion API call so TAB never hangs the shell.
func compCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 4*time.Second)
}

type completer = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)

// fixed completes a flag/argument from a static set of values.
func fixed(values ...string) completer {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return values, noFile
	}
}

// firstArgOnly applies fn to the first positional argument only.
func firstArgOnly(fn completer) completer {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return fn(cmd, args, toComplete)
		}
		return nil, noFile
	}
}

func completeProjects(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	ctx, cancel := compCtx()
	defer cancel()
	c := getClient()

	// Scope to the selected org (--org / `org use`), falling back to the
	// caller's default org — same as `project list` / `project use`.
	orgRef := rootCmd.Flag("org").Value.String()
	if orgRef == "" {
		me, err := c.GetMe(ctx)
		if err != nil || me.Organization == nil {
			return nil, noFile
		}
		orgRef = me.Organization.ID
	}

	projects, err := c.ListProjectsInOrg(ctx, orgRef)
	if err != nil {
		return nil, noFile
	}
	out := make([]string, 0, len(projects))
	for _, p := range projects {
		out = append(out, p.Name)
	}
	return out, noFile
}

func completeOrgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	ctx, cancel := compCtx()
	defer cancel()
	orgs, err := getClient().ListOrgs(ctx)
	if err != nil {
		return nil, noFile
	}
	out := make([]string, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, o.ShortID+"\t"+o.DisplayName)
	}
	return out, noFile
}

// completionProject resolves the project a sub-resource lives under, mirroring
// requireProject (the --project flag / current config).
func completionProject() (string, bool) {
	project := rootCmd.Flag("project").Value.String()
	return project, project != ""
}

func completeApps(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	project, ok := completionProject()
	if !ok {
		return nil, noFile
	}
	ctx, cancel := compCtx()
	defer cancel()
	apps, err := getClient().ListApps(ctx, project)
	if err != nil {
		return nil, noFile
	}
	out := make([]string, 0, len(apps))
	for _, a := range apps {
		out = append(out, a.Name+"\t"+a.Status)
	}
	return out, noFile
}

// completeAppIDs completes the --app flag, which takes an app ID; the name is
// shown as the description.
func completeAppIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	project, ok := completionProject()
	if !ok {
		return nil, noFile
	}
	ctx, cancel := compCtx()
	defer cancel()
	apps, err := getClient().ListApps(ctx, project)
	if err != nil {
		return nil, noFile
	}
	out := make([]string, 0, len(apps))
	for _, a := range apps {
		out = append(out, a.ID+"\t"+a.Name)
	}
	return out, noFile
}

func completeDatabases(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	project, ok := completionProject()
	if !ok {
		return nil, noFile
	}
	ctx, cancel := compCtx()
	defer cancel()
	dbs, err := getClient().ListDatabases(ctx, project)
	if err != nil {
		return nil, noFile
	}
	out := make([]string, 0, len(dbs))
	for _, d := range dbs {
		out = append(out, d.Name+"\t"+d.Status)
	}
	return out, noFile
}

func completeServiceAccounts(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	project, ok := completionProject()
	if !ok {
		return nil, noFile
	}
	ctx, cancel := compCtx()
	defer cancel()
	sas, err := getClient().ListServiceAccounts(ctx, project)
	if err != nil {
		return nil, noFile
	}
	out := make([]string, 0, len(sas))
	for _, sa := range sas {
		out = append(out, sa.ID+"\t"+sa.Email)
	}
	return out, noFile
}

func completeIAMBindings(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	project, ok := completionProject()
	if !ok {
		return nil, noFile
	}
	ctx, cancel := compCtx()
	defer cancel()
	bindings, err := getClient().ListIAMBindings(ctx, project)
	if err != nil {
		return nil, noFile
	}
	out := make([]string, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, b.ID+"\t"+b.Member+" ("+b.Role+")")
	}
	return out, noFile
}

// completeDomains completes a domain argument scoped to the --app flag.
func completeDomains(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	appID, _ := cmd.Flags().GetString("app")
	if appID == "" {
		return nil, noFile
	}
	ctx, cancel := compCtx()
	defer cancel()
	domains, err := getClient().ListDomains(ctx, appID)
	if err != nil {
		return nil, noFile
	}
	out := make([]string, 0, len(domains))
	for _, d := range domains {
		out = append(out, d.Domain+"\t"+d.Status)
	}
	return out, noFile
}

// completeConfigKeys completes a config key argument scoped to the --app flag.
func completeConfigKeys(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	appID, _ := cmd.Flags().GetString("app")
	if appID == "" {
		return nil, noFile
	}
	ctx, cancel := compCtx()
	defer cancel()
	cfgs, err := getClient().ListConfig(ctx, appID)
	if err != nil {
		return nil, noFile
	}
	out := make([]string, 0, len(cfgs))
	for _, c := range cfgs {
		out = append(out, c.Key)
	}
	return out, noFile
}

// registerCompletions attaches dynamic argument and flag completions across the
// command tree. Called from main() after every init() has run, so all commands
// and flags exist.
func registerCompletions() {
	// Positional resource arguments.
	for _, c := range []*cobra.Command{projectGetCmd, projectUseCmd, projectUpdateCmd, projectDeleteCmd} {
		c.ValidArgsFunction = completeProjects
	}
	for _, c := range []*cobra.Command{orgUseCmd, orgMembersCmd} {
		c.ValidArgsFunction = completeOrgs
	}
	for _, c := range []*cobra.Command{appGetCmd, appDeleteCmd, appLogsCmd, appRevisionsCmd, appScaleCmd, appRollbackCmd, appIdentityCmd, appTrafficCmd, appDeploymentsCmd} {
		c.ValidArgsFunction = completeApps
	}
	for _, c := range []*cobra.Command{dbGetCmd, dbUpdateCmd, dbDeleteCmd, dbBackupCreateCmd, dbBackupListCmd,
		dbBackupConfigShowCmd, dbBackupConfigSetCmd, dbBackupDestSetCmd, dbBackupDestShowCmd, dbBackupDestUnsetCmd, dbRestoreCmd} {
		c.ValidArgsFunction = completeDatabases
	}
	dbBackupDeleteCmd.ValidArgsFunction = firstArgOnly(completeDatabases)
	for _, c := range []*cobra.Command{saDeleteCmd, saKeysCreateCmd, saKeysListCmd} {
		c.ValidArgsFunction = completeServiceAccounts
	}
	// delete takes <sa-id> <key-id>; only complete service accounts for the first arg.
	saKeysDeleteCmd.ValidArgsFunction = firstArgOnly(completeServiceAccounts)
	iamRemoveCmd.ValidArgsFunction = completeIAMBindings
	for _, c := range []*cobra.Command{domainRemoveCmd, domainStatusCmd} {
		c.ValidArgsFunction = completeDomains
	}
	configUnsetCmd.ValidArgsFunction = completeConfigKeys

	// Persistent flags (defined on rootCmd).
	reg(rootCmd, "output", fixed("table", "json", "yaml"))
	reg(rootCmd, "org", completeOrgs)
	reg(rootCmd, "project", completeProjects)

	// Enum-style flags.
	for _, c := range []*cobra.Command{projectCreateCmd, projectUpdateCmd} {
		reg(c, "egress", fixed("restricted", "https", "all"))
		reg(c, "plan", fixed("starter", "standard", "premium"))
	}
	reg(appCreateCmd, "ingress", fixed("all", "internal"))
	for _, c := range []*cobra.Command{orgInviteCmd, orgAddUserCmd, orgSetRoleCmd, iamSetCmd} {
		reg(c, "role", fixed("owner", "editor", "viewer"))
	}
	reg(auditLogCmd, "resource-type", fixed("project", "organization"))

	// --org override on the org sub-commands shadows the persistent flag.
	for _, c := range []*cobra.Command{orgInviteCmd, orgAddUserCmd, orgSetRoleCmd, orgRemoveCmd} {
		reg(c, "org", completeOrgs)
	}

	// --app flags take an app ID.
	for _, c := range []*cobra.Command{appDeployCmd, domainAddCmd, domainRemoveCmd, domainListCmd, domainStatusCmd, configSetCmd, configListCmd, configUnsetCmd, webhookSetupCmd, webhookStatusCmd, webhookRemoveCmd} {
		reg(c, "app", completeAppIDs)
	}
}

// reg registers a flag completion, ignoring the "no such flag" error so the
// wiring stays declarative even if a flag is renamed.
func reg(cmd *cobra.Command, flag string, fn completer) {
	_ = cmd.RegisterFlagCompletionFunc(flag, fn)
}
