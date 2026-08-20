package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/charmbracelet/lipgloss"
	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

// website.go is the static-website facade (#342 layer 2, ADR-043): the simplest
// way to run static pages on fpcloud. A website IS a bucket with website
// serving enabled — these commands compose the bucket control plane and the
// `storage sync` data plane so a tenant never touches buckets/keys directly.

var websiteCmd = &cobra.Command{
	Use:     "website",
	Aliases: []string{"websites", "web"},
	Short:   "Host static websites (a thin facade over object storage)",
	Long: "Host static websites on fpcloud. A website is an object-storage bucket with\n" +
		"public web serving enabled; `website deploy` creates everything on first use.\n\n" +
		"  fpcloud website deploy mysite ./public\n" +
		"  # → https://mysite-<project>-<org>-web.fogpipe.cloud",
}

// websiteFlags reads the shared serving-convention flags. --spa sets the error
// document to the index document (the classic S3-website SPA fallback).
func websiteFlags(cmd *cobra.Command) (index, errorDoc string, spa bool) {
	index, _ = cmd.Flags().GetString("index")
	errorDoc, _ = cmd.Flags().GetString("error")
	spa, _ = cmd.Flags().GetBool("spa")
	if index == "" {
		index = "index.html"
	}
	if spa {
		errorDoc = index
	}
	return index, errorDoc, spa
}

// findWebsiteBucket returns the project's bucket named name, or nil.
func findWebsiteBucket(c *client.Client, name string) (*client.Bucket, error) {
	project, err := requireProject()
	if err != nil {
		return nil, err
	}
	buckets, err := c.ListBuckets(context.Background(), project)
	if err != nil {
		return nil, err
	}
	for _, b := range buckets {
		if b.Name == name {
			return b, nil
		}
	}
	return nil, nil
}

// ensureWebsite makes sure a bucket named name exists with web serving enabled
// (creating/enabling as needed) and returns it. The idempotent bootstrap behind
// `website create` and `website deploy`.
func ensureWebsite(c *client.Client, name, index, errorDoc string) (*client.Bucket, error) {
	b, err := findWebsiteBucket(c, name)
	if err != nil {
		return nil, err
	}
	if b == nil {
		project, err := requireProject()
		if err != nil {
			return nil, err
		}
		b, err = c.CreateBucket(context.Background(), project, client.CreateBucketRequest{Name: name})
		if err != nil {
			return nil, err
		}
	}
	if !b.WebsiteEnabled || b.WebsiteIndexDocument != index || b.WebsiteErrorDocument != errorDoc {
		b, err = c.SetBucketWebsite(context.Background(), b.ID, client.SetBucketWebsiteRequest{
			Enabled: true, IndexDocument: index, ErrorDocument: errorDoc,
		})
		if err != nil {
			return nil, err
		}
	}
	return b, nil
}

var websiteCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a website (a public web-served bucket)",
	Long: "Create a website: an object-storage bucket with public web serving enabled.\n" +
		"Content is world-readable over HTTP by definition — that is the product.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		index, errorDoc, _ := websiteFlags(cmd)
		c := getClient()

		outputFormat := rootCmd.Flag("output").Value.String()
		var b *client.Bucket
		var err error
		action := func() { b, err = ensureWebsite(c, args[0], index, errorDoc) }
		if !isStructured(outputFormat) {
			withSpinner("Creating website...", action)
		} else {
			action()
		}
		if err != nil {
			return err
		}
		if isStructured(outputFormat) {
			return renderData(b)
		}
		fmt.Println(renderInfoBox("Website Created", websiteInfoPairs(b)))
		fmt.Println(mutedStyle.Render("Deploy content with: fpcloud website deploy " + b.Name + " <dir>"))
		return nil
	},
}

var websiteDeployCmd = &cobra.Command{
	Use:   "deploy <name> <dir>",
	Short: "Deploy a directory to a website (creates the website on first use)",
	Long: "Sync a local directory to a website and serve it publicly. Creates the\n" +
		"website (bucket + web serving) if it doesn't exist yet.\n\n" +
		"The site mirrors the directory: files that no longer exist locally are\n" +
		"pruned from the site (opt out with --keep-extra).",
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		index, errorDoc, _ := websiteFlags(cmd)
		keepExtra, _ := cmd.Flags().GetBool("keep-extra")
		dryrun, _ := cmd.Flags().GetBool("dryrun")
		rules := *(cmd.Context().Value(filterCtxKey).(*[]filterRule))
		c := getClient()

		b, err := ensureWebsite(c, args[0], index, errorDoc)
		if err != nil {
			return err
		}
		// Upload to an immutable, internal version prefix (#439). Deploys are
		// atomic: nothing is served from the new version until PublishWebsiteVersion
		// flips the Ingress rewrite + Garage error_document onto it. The version
		// never appears in the public URL.
		next := b.WebsiteVersion + 1
		// Web cache defaults are on here and nowhere else: this is the path whose
		// objects are served over HTTP, so a policy is wanted. A plain
		// `storage cp` of a data object should not silently acquire one.
		ccFlags, _ := cmd.Flags().GetStringArray("cache-control")
		cacheRules, err := parseCacheControlFlags(ccFlags)
		if err != nil {
			return err
		}
		if err := runSync(args[1], fmt.Sprintf("fps://%s/v%d/", b.Name, next), syncOptions{
			del: !keepExtra, dryrun: dryrun, rules: rules,
			put: putOpts{webCache: true, cacheRules: cacheRules},
		}); err != nil {
			return err
		}
		if dryrun {
			return nil
		}
		b, err = c.PublishWebsiteVersion(context.Background(), b.ID, next)
		if err != nil {
			return err
		}
		// Retention: prune versions beyond --keep (never the live one). Best-effort
		// — a GC failure must not fail an otherwise-successful deploy.
		keep, _ := cmd.Flags().GetInt("keep")
		if err := pruneWebsiteVersions(context.Background(), c, b, keep); err != nil {
			fmt.Println(mutedStyle.Render("note: could not prune old versions: " + err.Error()))
		}
		url := b.WebsiteURL
		if url == "" {
			url = mutedStyle.Render("(served host not configured on this API)")
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Deployed %s → %s (v%d)", args[1], lipgloss.NewStyle().Bold(true).Foreground(colorInfo).Render(url), next),
		))
		return nil
	},
}

// defaultWebsiteVersionsKept is how many recent website versions retention keeps
// (older ones are pruned after a deploy) when --keep is not given.
const defaultWebsiteVersionsKept = 5

// websiteVersionRe matches a top-level published-version prefix "v<N>/".
var websiteVersionRe = regexp.MustCompile(`^v(\d+)/$`)

// listWebsiteVersions returns the version numbers physically present in the
// bucket (its top-level v<N>/ prefixes), ascending.
func listWebsiteVersions(ctx context.Context, c *client.Client, name string) ([]int, error) {
	var versions []int
	err := retryOnAuth(func(refresh bool) error {
		cli, bkt, err := bucketS3Client(ctx, c, name, refresh)
		if err != nil {
			return err
		}
		versions = versions[:0]
		return listObjects(ctx, cli, bkt, "v", "/",
			func(s3types.Object) error { return nil },
			func(prefix string) error {
				if m := websiteVersionRe.FindStringSubmatch(prefix); m != nil {
					n, _ := strconv.Atoi(m[1])
					versions = append(versions, n)
				}
				return nil
			})
	})
	if err != nil {
		return nil, err
	}
	sort.Ints(versions)
	return versions, nil
}

// pruneWebsiteVersions deletes version prefixes beyond the newest keep, never
// touching the currently-served version (which may be older than newest after a
// rollback).
func pruneWebsiteVersions(ctx context.Context, c *client.Client, b *client.Bucket, keep int) error {
	if keep < 1 {
		keep = defaultWebsiteVersionsKept
	}
	versions, err := listWebsiteVersions(ctx, c, b.Name)
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.IntSlice(versions)))
	var toDelete []int
	for i, v := range versions {
		if i < keep || v == b.WebsiteVersion {
			continue
		}
		toDelete = append(toDelete, v)
	}
	if len(toDelete) == 0 {
		return nil
	}
	return retryOnAuth(func(refresh bool) error {
		cli, bkt, err := bucketS3Client(ctx, c, b.Name, refresh)
		if err != nil {
			return err
		}
		for _, v := range toDelete {
			if err := deleteWebsiteVersion(ctx, cli, bkt, v); err != nil {
				return err
			}
		}
		return nil
	})
}

// deleteWebsiteVersion removes every object under the v<version>/ prefix in
// batches of up to 1000 (the DeleteObjects limit).
func deleteWebsiteVersion(ctx context.Context, cli *s3.Client, bucket string, version int) error {
	prefix := fmt.Sprintf("v%d/", version)
	var ids []s3types.ObjectIdentifier
	flush := func() error {
		if len(ids) == 0 {
			return nil
		}
		_, err := cli.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: &bucket,
			Delete: &s3types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		})
		ids = ids[:0]
		return err
	}
	if err := listObjects(ctx, cli, bucket, prefix, "", func(o s3types.Object) error {
		ids = append(ids, s3types.ObjectIdentifier{Key: o.Key})
		if len(ids) >= 1000 {
			return flush()
		}
		return nil
	}, nil); err != nil {
		return err
	}
	return flush()
}

var websiteRollbackCmd = &cobra.Command{
	Use:   "rollback <name>",
	Short: "Roll back a website to a previously deployed version",
	Long: "Instantly re-point a website at a retained prior version — no re-upload,\n" +
		"no rebuild. Defaults to the version before the current one; --to picks a\n" +
		"specific version. See available versions with `website show`.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		b, err := requireWebsite(c, args[0])
		if err != nil {
			return err
		}
		if b.WebsiteVersion < 1 {
			return fmt.Errorf("website %q has no deployed version to roll back from", args[0])
		}
		target, _ := cmd.Flags().GetInt("to")
		if target == 0 {
			target = b.WebsiteVersion - 1
		}
		if target < 1 {
			return fmt.Errorf("no earlier version to roll back to (current is v%d)", b.WebsiteVersion)
		}
		if target == b.WebsiteVersion {
			return fmt.Errorf("website is already serving v%d", target)
		}
		versions, err := listWebsiteVersions(context.Background(), c, b.Name)
		if err != nil {
			return err
		}
		if !slices.Contains(versions, target) {
			return fmt.Errorf("version v%d is not available (retained: %v)", target, versions)
		}
		b, err = c.PublishWebsiteVersion(context.Background(), b.ID, target)
		if err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Rolled back %s to v%d → %s", args[0], target, lipgloss.NewStyle().Bold(true).Foreground(colorInfo).Render(b.WebsiteURL)),
		))
		return nil
	},
}

var websiteListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List the current project's websites",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		c := getClient()
		buckets, err := c.ListBuckets(context.Background(), projectID)
		if err != nil {
			return err
		}
		sites := make([]*client.Bucket, 0, len(buckets))
		for _, b := range buckets {
			if b.WebsiteEnabled {
				sites = append(sites, b)
			}
		}
		rows := make([][]string, len(sites))
		for i, b := range sites {
			rows[i] = []string{b.Name, dashIfEmpty(b.WebsiteURL), renderStatus(b.Status)}
		}
		render([]string{"NAME", "URL", "STATUS"}, rows, sites)
		return nil
	},
}

var websiteShowCmd = &cobra.Command{
	Use:     "show <name>",
	Aliases: []string{"get", "status"},
	Short:   "Show a website's details",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		b, err := findWebsiteBucket(c, args[0])
		if err != nil {
			return err
		}
		if b == nil {
			return notFoundf("website %q not found in the current project", args[0])
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(b)
		}
		fmt.Println(renderInfoBox("Website", websiteInfoPairs(b)))
		return nil
	},
}

var websiteUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update a website's serving conventions or vanity slug",
	Long: "Update a website's index/error documents, SPA fallback, or vanity slug.\n" +
		"--slug moves the site to <slug>.web.<platform-domain> (globally unique);\n" +
		"--slug \"\" reverts to the derived host.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conventionsChanged := cmd.Flags().Changed("index") || cmd.Flags().Changed("error") || cmd.Flags().Changed("spa")
		slugChanged := cmd.Flags().Changed("slug")
		if !conventionsChanged && !slugChanged {
			return fmt.Errorf("nothing to update: set --index/--error/--spa and/or --slug")
		}
		c := getClient()
		b, err := findWebsiteBucket(c, args[0])
		if err != nil {
			return err
		}
		if b == nil {
			return notFoundf("website %q not found in the current project", args[0])
		}
		if conventionsChanged {
			index, errorDoc, _ := websiteFlags(cmd)
			b, err = c.SetBucketWebsite(context.Background(), b.ID, client.SetBucketWebsiteRequest{
				Enabled: true, IndexDocument: index, ErrorDocument: errorDoc,
			})
			if err != nil {
				return err
			}
		}
		if slugChanged {
			slug, _ := cmd.Flags().GetString("slug")
			b, err = c.SetBucketURLSlug(context.Background(), b.ID, slug)
			if err != nil {
				return err
			}
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(b)
		}
		fmt.Println(renderInfoBox("Website Updated", websiteInfoPairs(b)))
		return nil
	},
}

var websiteDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a website and its content",
	Long: "Delete a website: removes the site's content and the underlying bucket.\n" +
		"Without --force, a non-empty site is refused (like bucket delete).",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			ok, err := confirm(
				fmt.Sprintf("Delete website %q?", args[0]),
				"The site goes offline and its content is deleted. This cannot be undone.",
				"Yes, delete")
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}

		c := getClient()
		b, err := findWebsiteBucket(c, args[0])
		if err != nil {
			return err
		}
		if b == nil {
			return notFoundf("website %q not found in the current project", args[0])
		}
		if force {
			if err := emptyBucket(c, b.Name); err != nil {
				return fmt.Errorf("empty website content: %w", err)
			}
		}
		if err := c.DeleteBucket(context.Background(), b.ID); err != nil {
			var apiErr *client.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
				return fmt.Errorf("website %q still has content: re-run with --force to delete everything", args[0])
			}
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Website %q deleted.", args[0]),
		))
		return nil
	},
}

var websiteDomainCmd = &cobra.Command{
	Use:     "domain",
	Aliases: []string{"domains"},
	Short:   "Custom domains for a website",
}

// requireWebsite resolves a website name to its bucket, erroring when absent.
func requireWebsite(c *client.Client, name string) (*client.Bucket, error) {
	b, err := findWebsiteBucket(c, name)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, notFoundf("website %q not found in the current project", name)
	}
	return b, nil
}

var websiteDomainAddCmd = &cobra.Command{
	Use:   "add <website> <domain>",
	Short: "Attach a custom domain to a website",
	Long: "Attach a custom domain (e.g. www.example.com) to a website. The domain\n" +
		"starts unverified: add the printed TXT + pointing records at your DNS\n" +
		"provider, then run `website domain status` to advance it. TLS issues\n" +
		"automatically once verification passes.",
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		b, err := requireWebsite(c, args[0])
		if err != nil {
			return err
		}
		if _, err := c.AddBucketDomain(context.Background(), b.ID, args[1]); err != nil {
			return err
		}
		vr, err := c.VerifyBucketDomain(context.Background(), b.ID, args[1])
		if err != nil {
			return err
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(vr)
		}
		renderDomainVerification(vr)
		return nil
	},
}

var websiteDomainListCmd = &cobra.Command{
	Use:     "list <website>",
	Aliases: []string{"ls"},
	Short:   "List a website's custom domains",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		b, err := requireWebsite(c, args[0])
		if err != nil {
			return err
		}
		domains, err := c.ListBucketDomains(context.Background(), b.ID)
		if err != nil {
			return err
		}
		rows := make([][]string, len(domains))
		for i, d := range domains {
			rows[i] = []string{d.Domain, renderStatus(d.Status), d.TLSStatus}
		}
		render([]string{"DOMAIN", "STATUS", "TLS"}, rows, domains)
		return nil
	},
}

var websiteDomainStatusCmd = &cobra.Command{
	Use:     "status <website> <domain>",
	Aliases: []string{"verify"},
	Short:   "Re-check a domain's ownership/pointing/TLS and show what's missing",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		b, err := requireWebsite(c, args[0])
		if err != nil {
			return err
		}
		vr, err := c.VerifyBucketDomain(context.Background(), b.ID, args[1])
		if err != nil {
			return err
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(vr)
		}
		renderDomainVerification(vr)
		return nil
	},
}

var websiteDomainRemoveCmd = &cobra.Command{
	Use:   "remove <website> <domain>",
	Short: "Detach a custom domain from a website",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		b, err := requireWebsite(c, args[0])
		if err != nil {
			return err
		}
		if err := c.RemoveBucketDomain(context.Background(), b.ID, args[1]); err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Domain %q removed.", args[1]),
		))
		return nil
	},
}

// emptyBucket deletes every object in the bucket via the data plane (the
// engine behind `website delete --force`).
func emptyBucket(c *client.Client, bucketRef string) error {
	ctx := context.Background()
	return retryOnAuth(func(force bool) error {
		cli, s3bucket, err := bucketS3Client(ctx, c, bucketRef, force)
		if err != nil {
			return err
		}
		var batch []s3types.ObjectIdentifier
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			_, err := cli.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: &s3bucket,
				Delete: &s3types.Delete{Objects: batch, Quiet: aws.Bool(true)},
			})
			batch = nil
			return err
		}
		err = listObjects(ctx, cli, s3bucket, "", "", func(o s3types.Object) error {
			batch = append(batch, s3types.ObjectIdentifier{Key: o.Key})
			if len(batch) >= 1000 {
				return flush()
			}
			return nil
		}, nil)
		if err != nil {
			return err
		}
		return flush()
	})
}

func init() {
	for _, cmd := range []*cobra.Command{websiteCreateCmd, websiteDeployCmd, websiteUpdateCmd} {
		cmd.Flags().String("index", "", "Index document served for a directory request (default index.html)")
		cmd.Flags().String("error", "", "Document served on a miss (e.g. 404.html)")
		cmd.Flags().Bool("spa", false, "Single-page app: serve the index document on misses (client-side routing)")
	}

	websiteUpdateCmd.Flags().String("slug", "", "Vanity host label: site moves to <slug>.web.<platform-domain> (\"\" reverts)")

	websiteDeployCmd.Flags().Bool("keep-extra", false, "Keep site files that no longer exist locally (default prunes)")
	websiteDeployCmd.Flags().Bool("dryrun", false, "Print what would change without transferring")
	websiteDeployCmd.Flags().Int("keep", defaultWebsiteVersionsKept, "Number of recent versions to retain for rollback (older ones are pruned after deploy)")
	websiteDeployCmd.Flags().StringArray("cache-control", nil, cacheControlFlagHelp+
		" — overrides the defaults (fingerprinted assets immutable, HTML no-cache)")
	websiteRollbackCmd.Flags().Int("to", 0, "Specific version to roll back to (default: the version before current)")
	deployFilters := addFilterFlags(websiteDeployCmd)
	websiteDeployCmd.PreRun = func(cmd *cobra.Command, args []string) {
		cmd.SetContext(context.WithValue(cmd.Context(), filterCtxKey, deployFilters))
	}

	websiteDeleteCmd.Flags().Bool("force", false, "Delete all site content first")
	websiteDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	websiteDomainCmd.AddCommand(websiteDomainAddCmd, websiteDomainListCmd, websiteDomainStatusCmd, websiteDomainRemoveCmd)
	websiteCmd.AddCommand(websiteCreateCmd, websiteDeployCmd, websiteListCmd, websiteShowCmd, websiteUpdateCmd, websiteRollbackCmd, websiteDeleteCmd, websiteDomainCmd)
	rootCmd.AddCommand(websiteCmd)
}
