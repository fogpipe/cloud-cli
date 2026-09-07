package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// registryHost is the self-hosted Zot registry. Under the ADR-013 token broker the
// registry is bearer-only: Docker exchanges the credential fpcloud presents for a
// short-lived, IAM-scoped registry token at the broker's `/token` endpoint. fpcloud
// presents the caller's OWN fpcloud identity — there is no shared registry password
// and no cluster/apiserver access on the credential path.
const registryHost = "registry.cloud.fogpipe.com"

// fetchRegistryCreds returns the (username, password) fpcloud hands to Docker for
// the bearer registry. The password is the caller's fpcloud credential, which the
// broker validates and exchanges for a scoped token: a service-account API key
// (FPCLOUD_API_KEY — e.g. minted in CI by OIDC federation via `fogpipe/cloud-auth`)
// when set, otherwise the ID token from `fpcloud login` (auto-refreshed).
// The username is a Docker-side label the broker ignores — identity is the password.
// registryPushWindow is how long the credential handed to docker must stay
// valid: docker fetches it once and re-mints registry tokens with it until the
// push's last request, so a credential that expired mid-push ended an
// eight-minute push with `unauthorized` at the manifest (fogpipe/cloud-workspace#285).
// The broker bounds the tokens it mints to this credential's remaining life.
const registryPushWindow = time.Hour

func fetchRegistryCreds() (username, password string, err error) {
	if key := os.Getenv("FPCLOUD_API_KEY"); key != "" {
		return dockerCredHelperName, key, nil
	}
	token, err := idTokenValidFor(registryPushWindow)
	if err != nil {
		return "", "", fmt.Errorf("no registry credential — run `fpcloud login` or set FPCLOUD_API_KEY: %w", err)
	}
	return dockerCredHelperName, token, nil
}

var registryCmd = &cobra.Command{
	Use:   "registry",
	Short: "Manage the fpcloud container registry",
}

var registryGetLoginPasswordCmd = &cobra.Command{
	Use:   "get-login-password",
	Short: "Print the registry password (pipe into `docker login --password-stdin`)",
	Long: "Print the registry password to stdout. Mirrors `aws ecr get-login-password`:\n\n" +
		"  fpcloud registry get-login-password | docker login --username fogpipe --password-stdin " + registryHost,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, password, err := fetchRegistryCreds()
		if err != nil {
			return err
		}
		fmt.Println(password)
		return nil
	},
}

var registryLoginCmd = &cobra.Command{
	Use:   "login [REGISTRY...]",
	Short: "Let Docker push to " + registryHost + " as you",
	Long: "Configure Docker to authenticate to the fpcloud container registry with your\n" +
		"fpcloud identity. Registers fpcloud as a Docker credential helper (a `credHelpers`\n" +
		"entry in ~/.docker/config.json plus a `docker-credential-fpcloud` link beside the\n" +
		"binary), so every `docker push`/`pull` fetches a fresh short-lived token — no\n" +
		"`docker login`, no static password on disk, nothing that expires an hour later.\n\n" +
		"Defaults to " + registryHost + "; pass extra hosts to configure them too.\n" +
		"Where a helper cannot be installed (a CI job running the CLI with `go run`), pipe\n" +
		"`fpcloud registry get-login-password` into `docker login --password-stdin` instead.",
	Example: "  fpcloud registry login",
	RunE: func(cmd *cobra.Command, args []string) error {
		hosts := args
		if len(hosts) == 0 {
			hosts = []string{registryHost}
		}

		link, linkErr := ensureDockerCredentialHelper()
		cfgPath, err := addDockerCredHelpers(hosts)
		if err != nil {
			return err
		}

		ok := lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓")
		fmt.Println(ok + " Docker configured to use fpcloud for:")
		for _, h := range hosts {
			fmt.Println(mutedStyle.Render("    " + h))
		}
		fmt.Println(mutedStyle.Render("  Updated " + cfgPath))
		if linkErr != nil {
			exe, _ := os.Executable()
			fmt.Println()
			fmt.Println(lipgloss.NewStyle().Foreground(colorWarning).Render(
				"  Could not install the docker-credential-fpcloud helper automatically:"))
			fmt.Println(mutedStyle.Render("    " + linkErr.Error()))
			fmt.Println(mutedStyle.Render("  Create it manually on your PATH (point it at the fpcloud binary), e.g.:"))
			fmt.Println(mutedStyle.Render(fmt.Sprintf("    ln -s %q ~/.local/bin/%s%s", exe, dockerCredHelperPrefix, dockerCredHelperName)))
		} else {
			fmt.Println(mutedStyle.Render("  Helper: " + link))
		}
		fmt.Println()
		fmt.Println(mutedStyle.Render("  Now: docker push " + registryHost + "/<org>/<project>/<app>:<tag>"))
		return nil
	},
}

var registryReposCmd = &cobra.Command{
	Use:     "repos",
	Aliases: []string{"repo", "repositories"},
	Short:   "Browse the current project's image repositories",
}

var registryReposListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List image repositories in the current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		repos, err := getClient().ListRegistryRepositories(context.Background(), projectID)
		if err != nil {
			return err
		}
		// The size is what the registry holds for the repository right now,
		// deduplicated — the reading the org's registry ceiling is refused
		// against (ADR-128), so a tenant told to delete images can see which
		// (fogpipe/cloud-workspace#284). A repository the registry could not
		// size says so rather than reading as empty.
		rows := make([][]string, len(repos))
		for i, r := range repos {
			rows[i] = []string{r.Name, repoSize(r)}
		}
		render([]string{"REPOSITORY", "SIZE"}, rows, repos)
		return nil
	},
}

var registryTagsCmd = &cobra.Command{
	Use:     "tags",
	Aliases: []string{"tag"},
	Short:   "Browse and delete image tags",
}

var registryTagsListCmd = &cobra.Command{
	Use:     "list <repo>",
	Aliases: []string{"ls"},
	Short:   "List a repository's image tags",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		tags, err := getClient().ListRegistryTags(context.Background(), projectID, args[0])
		if err != nil {
			return err
		}
		rows := make([][]string, len(tags.Tags))
		for i, t := range tags.Tags {
			rows[i] = []string{t}
		}
		render([]string{"TAG"}, rows, tags)
		return nil
	},
}

var registryCVEsCmd = &cobra.Command{
	Use:     "cves <repo> <tag>",
	Aliases: []string{"cve", "vulnerabilities"},
	Short:   "List the CVEs the registry's scanner found in one image",
	Long: `List the CVEs the registry's scanner found in one image.

Each row is one CVE in one package: the package and the version the image
carries, and the version that fixes it — empty when no upstream fix exists,
which is the row to assess for reachability rather than bump. Most severe
first, then by package.

  fpcloud registry cves web v42
  fpcloud registry cves web v42 -o json`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		list, err := getClient().ListRegistryCVEs(context.Background(), projectID, args[0], args[1])
		if err != nil {
			return err
		}
		var rows [][]string
		for _, cve := range list.CVEs {
			if len(cve.Packages) == 0 {
				rows = append(rows, []string{cve.Severity, cve.ID, "", "", "", cve.Title})
			}
			for _, p := range cve.Packages {
				rows = append(rows, []string{cve.Severity, cve.ID, p.Name, p.InstalledVersion, p.FixedVersion, cve.Title})
			}
		}
		render([]string{"SEVERITY", "CVE", "PACKAGE", "INSTALLED", "FIXED", "TITLE"}, rows, list)
		return nil
	},
}

var registryTagsDeleteCmd = &cobra.Command{
	Use:     "delete <repo> <tag>",
	Aliases: []string{"rm"},
	Short:   "Delete one image tag from a repository",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		if err := getClient().DeleteRegistryTag(context.Background(), projectID, args[0], args[1]); err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Tag %s:%s deleted.", args[0], args[1]),
		))
		return nil
	},
}

// retentionRepoLabel names a policy's scope in tables: the project-wide default
// policy has an empty repo.
func retentionRepoLabel(repo string) string {
	if repo == "" {
		return "(project default)"
	}
	return repo
}

var registryRetentionCmd = &cobra.Command{
	Use:   "retention",
	Short: "Manage image auto-delete (retention) policies",
}

var registryRetentionListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List the current project's retention policies",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		policies, err := getClient().ListRetentionPolicies(context.Background(), projectID)
		if err != nil {
			return err
		}
		rows := make([][]string, len(policies))
		for i, p := range policies {
			keep, maxAge := "—", "—"
			if p.KeepLast > 0 {
				keep = strconv.Itoa(p.KeepLast)
			}
			if p.MaxAgeDays > 0 {
				maxAge = strconv.Itoa(p.MaxAgeDays) + "d"
			}
			rows[i] = []string{retentionRepoLabel(p.Repo), keep, maxAge, yesNo(p.Enabled)}
		}
		render([]string{"REPOSITORY", "KEEP LAST", "MAX AGE", "ENABLED"}, rows, policies)
		return nil
	},
}

var registryRetentionSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set a retention policy (project default, or one repo with --repo)",
	Long: "Set a retention policy for the current project. Without --repo the policy is the\n" +
		"project-wide default; --repo overrides it for one repository. --keep-last keeps the\n" +
		"newest N tags; --max-age-days deletes tags older than N days (the newest --keep-last\n" +
		"are always protected). The hourly sweeper enforces enabled policies; use\n" +
		"`retention preview` for a dry run and `retention apply` to enforce now.",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		repo, _ := cmd.Flags().GetString("repo")
		keepLast, _ := cmd.Flags().GetInt("keep-last")
		maxAgeDays, _ := cmd.Flags().GetInt("max-age-days")
		disabled, _ := cmd.Flags().GetBool("disabled")

		policy, err := getClient().SetRetentionPolicy(context.Background(), projectID, client.SetRetentionPolicyRequest{
			Repo:       repo,
			KeepLast:   keepLast,
			MaxAgeDays: maxAgeDays,
			Enabled:    !disabled,
		})
		if err != nil {
			return err
		}

		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(policy)
		}
		fmt.Println(renderInfoBox("Retention Policy Set", [][]string{
			{"Scope", retentionRepoLabel(policy.Repo)},
			{"Keep last", strconv.Itoa(policy.KeepLast)},
			{"Max age (days)", strconv.Itoa(policy.MaxAgeDays)},
			{"Enabled", yesNo(policy.Enabled)},
		}))
		return nil
	},
}

var registryRetentionDeleteCmd = &cobra.Command{
	Use:     "delete [repo]",
	Aliases: []string{"rm"},
	Short:   "Remove a retention policy (no repo = the project default)",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		repo := ""
		if len(args) == 1 {
			repo = args[0]
		}
		if err := getClient().DeleteRetentionPolicy(context.Background(), projectID, repo); err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Retention policy for %s removed.", retentionRepoLabel(repo)),
		))
		return nil
	},
}

// renderRetentionItems prints the tags a retention run selected (or would).
func renderRetentionItems(preview *client.RetentionPreview) {
	rows := make([][]string, len(preview.Items))
	for i, item := range preview.Items {
		// "First seen", not "pushed": this is when fpcloud first held the
		// manifest, which is all a registry can honestly report — an image's own
		// timestamp is written by whatever built it.
		firstSeen := "—"
		if item.FirstSeenAt != nil {
			firstSeen = item.FirstSeenAt.Format("2006-01-02 15:04")
		}
		rows[i] = []string{item.Repo, item.Tag, item.Reason, firstSeen}
	}
	render([]string{"REPOSITORY", "TAG", "REASON", "FIRST SEEN"}, rows, preview)
}

var registryRetentionPreviewCmd = &cobra.Command{
	Use:   "preview",
	Short: "Dry-run: list the tags the project's policies would delete now",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		preview, err := getClient().PreviewRetention(context.Background(), projectID)
		if err != nil {
			return err
		}
		renderRetentionItems(preview)
		return nil
	},
}

var registryRetentionApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Enforce the project's retention policies now, deleting selected tags",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			ok, err := confirm(
				"Apply retention policies now?",
				"Selected tags are deleted from the registry. Run `retention preview` first to see them.",
				"Yes, delete")
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		deleted, err := getClient().ApplyRetention(context.Background(), projectID)
		if err != nil {
			return err
		}
		renderRetentionItems(deleted)
		return nil
	},
}

var registryVisibilityCmd = &cobra.Command{
	Use:   "visibility",
	Short: "Manage per-repository public/private visibility",
}

var registryVisibilityGetCmd = &cobra.Command{
	Use:     "get",
	Aliases: []string{"list", "ls"},
	Short:   "Show the current project's repository visibility (unlisted repos are private)",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		records, err := getClient().ListRegistryVisibility(context.Background(), projectID)
		if err != nil {
			return err
		}
		rows := make([][]string, len(records))
		for i, rec := range records {
			visibility := "private"
			if rec.Public {
				visibility = "public"
			}
			rows[i] = []string{rec.Repo, visibility}
		}
		render([]string{"REPOSITORY", "VISIBILITY"}, rows, records)
		return nil
	},
}

var registryVisibilitySetCmd = &cobra.Command{
	Use:   "set <repo> <public|private>",
	Short: "Make a repository anonymously pullable, or private again",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var public bool
		switch args[1] {
		case "public":
			public = true
		case "private":
			public = false
		default:
			return fmt.Errorf("visibility must be %q or %q, got %q", "public", "private", args[1])
		}
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		record, err := getClient().SetRegistryVisibility(context.Background(), projectID, client.SetRegistryVisibilityRequest{
			Repo:   args[0],
			Public: public,
		})
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(record)
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Repository %q is now %s.", args[0], args[1]),
		))
		return nil
	},
}

func init() {
	registryReposCmd.AddCommand(registryReposListCmd)
	registryTagsCmd.AddCommand(registryTagsListCmd, registryTagsDeleteCmd)

	registryRetentionSetCmd.Flags().String("repo", "", "Repository the policy applies to (empty = project-wide default)")
	registryRetentionSetCmd.Flags().Int("keep-last", 0, "Keep the newest N tags (0 = no count limit)")
	registryRetentionSetCmd.Flags().Int("max-age-days", 0, "Delete tags older than N days (0 = no age limit)")
	registryRetentionSetCmd.Flags().Bool("disabled", false, "Store the policy without enforcing it")
	registryRetentionApplyCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registryRetentionCmd.AddCommand(
		registryRetentionListCmd, registryRetentionSetCmd, registryRetentionDeleteCmd,
		registryRetentionPreviewCmd, registryRetentionApplyCmd,
	)

	registryVisibilityCmd.AddCommand(registryVisibilityGetCmd, registryVisibilitySetCmd)

	registryCmd.AddCommand(
		registryGetLoginPasswordCmd, registryLoginCmd,
		registryReposCmd, registryTagsCmd, registryCVEsCmd, registryRetentionCmd, registryVisibilityCmd,
	)
	rootCmd.AddCommand(registryCmd)
}

func repoSize(r client.RegistryRepository) string {
	if r.Bytes == nil {
		return mutedStyle.Render("? (" + firstNonEmpty(r.Error, "not sized") + ")")
	}
	return humanizeSize(*r.Bytes)
}
