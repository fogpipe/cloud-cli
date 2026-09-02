package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

// storageCmd is the parent for the managed object-storage (Garage, ADR-039)
// control-plane commands. Object data-plane verbs (ls/cp/rm/…) hang off the same
// parent in #266.
var storageCmd = &cobra.Command{
	Use:     "storage",
	Aliases: []string{"st"},
	Short:   "Manage object-storage buckets, scoped keys, and app bindings",
}

var storageBucketCmd = &cobra.Command{
	Use:     "bucket",
	Aliases: []string{"buckets", "b"},
	Short:   "Manage object-storage buckets",
}

var storageKeysCmd = &cobra.Command{
	Use:     "keys",
	Aliases: []string{"key"},
	Short:   "Manage a bucket's scoped S3 access keys",
}

// resolveBucketID turns a user-supplied bucket reference (name or id) into a
// bucket id. A UUID is returned as-is (and needs no project); a plain name is
// looked up in the current project, where bucket names are unique.
func resolveBucketID(c *client.Client, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("bucket name or id is required")
	}
	if looksLikeUUID(ref) {
		return ref, nil
	}
	project, err := requireProject()
	if err != nil {
		return "", fmt.Errorf("resolve bucket %q: %w", ref, err)
	}
	buckets, err := c.ListBuckets(context.Background(), project)
	if err != nil {
		return "", err
	}
	for _, b := range buckets {
		if b.Name == ref {
			return b.ID, nil
		}
	}
	return "", notFoundf("bucket %q not found in project %q", ref, project)
}

// parseSize parses a byte count that may carry a binary suffix (Ki/Mi/Gi/Ti). An
// empty string (or 0) means unlimited and returns 0.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	multipliers := []struct {
		suffix string
		factor int64
	}{
		{"Ti", 1 << 40},
		{"Gi", 1 << 30},
		{"Mi", 1 << 20},
		{"Ki", 1 << 10},
	}
	for _, m := range multipliers {
		if strings.HasSuffix(s, m.suffix) {
			n, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, m.suffix)), 64)
			if err != nil {
				return 0, fmt.Errorf("invalid size %q: %w", s, err)
			}
			return int64(n * float64(m.factor)), nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: want a byte count or a Ki/Mi/Gi/Ti suffix", s)
	}
	return n, nil
}

// humanizeQuota renders a size bound; 0 is "unlimited".
func humanizeQuota(n int64) string {
	if n <= 0 {
		return "unlimited"
	}
	return humanizeSize(n)
}

// humanizeSize renders a byte count with a binary suffix.
func humanizeSize(n int64) string {
	if n <= 0 {
		return "0B"
	}
	units := []struct {
		suffix string
		factor int64
	}{
		{"Ti", 1 << 40},
		{"Gi", 1 << 30},
		{"Mi", 1 << 20},
		{"Ki", 1 << 10},
	}
	for _, u := range units {
		if n >= u.factor {
			v := float64(n) / float64(u.factor)
			return strings.TrimSuffix(strconv.FormatFloat(v, 'f', 1, 64), ".0") + u.suffix
		}
	}
	return strconv.FormatInt(n, 10) + "B"
}

// yesNo renders a bool as a short yes/no marker.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

var storageBucketCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create an object-storage bucket",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		// A flag left off is left off, rather than sent as a zero: absent takes
		// the platform default and 0 is unlimited, which only an operator may
		// declare (ADR-128).
		var maxSize, maxObjects *int64
		if cmd.Flags().Changed("quota-size") {
			sizeStr, _ := cmd.Flags().GetString("quota-size")
			v, err := parseSize(sizeStr)
			if err != nil {
				return err
			}
			maxSize = &v
		}
		if cmd.Flags().Changed("quota-objects") {
			v, _ := cmd.Flags().GetInt64("quota-objects")
			maxObjects = &v
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		c := getClient()

		var b *client.Bucket
		var createErr error
		action := func() {
			b, createErr = c.CreateBucket(context.Background(), projectID, client.CreateBucketRequest{
				Name:            args[0],
				QuotaMaxSize:    maxSize,
				QuotaMaxObjects: maxObjects,
			})
		}
		if !isStructured(outputFormat) {
			withSpinner("Creating bucket...", action)
		} else {
			action()
		}
		if createErr != nil {
			return createErr
		}

		if isStructured(outputFormat) {
			return renderData(b)
		}

		pairs := [][]string{
			{"ID", mutedStyle.Render(b.ID)},
			{"Name", b.Name},
			{"S3 name", bucketS3Name(b)},
			{"Endpoint", b.Endpoint},
			{"Region", b.Region},
			{"Quota", bucketQuota(b.QuotaMaxSize, b.QuotaMaxObjects)},
			{"Status", renderStatus(b.Status)},
		}
		if b.AccessKeyID != "" {
			pairs = append(pairs, []string{"Access Key", b.AccessKeyID})
		}
		if b.SecretAccessKey != "" {
			pairs = append(pairs,
				[]string{"Secret Key", lipgloss.NewStyle().Bold(true).Foreground(colorInfo).Render(b.SecretAccessKey)},
				[]string{"", mutedStyle.Render("Shown once — store it now; it is not retrievable later.")},
			)
		}
		fmt.Println(renderInfoBox("Bucket Created", pairs))
		return nil
	},
}

var storageBucketListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List buckets in the current project",
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
		rows := make([][]string, len(buckets))
		for i, b := range buckets {
			rows[i] = []string{b.Name, bucketUsage(b), b.Region, renderStatus(b.Status)}
		}
		render([]string{"NAME", "USED/QUOTA", "REGION", "STATUS"}, rows, buckets)
		return nil
	},
}

var storageBucketDescribeCmd = &cobra.Command{
	Use:     "get <name|id>",
	Aliases: []string{"info"},
	Short:   "Show bucket details",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		b, err := c.GetBucket(context.Background(), id)
		if err != nil {
			return err
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(b)
		}
		accessKey := b.AccessKeyID
		if accessKey == "" {
			accessKey = mutedStyle.Render("—")
		}
		fmt.Println(renderInfoBox("Bucket Details", [][]string{
			{"ID", mutedStyle.Render(b.ID)},
			{"Name", b.Name},
			{"S3 name", bucketS3Name(b)},
			{"Endpoint", b.Endpoint},
			{"Region", b.Region},
			{"Quota", bucketQuota(b.QuotaMaxSize, b.QuotaMaxObjects)},
			{"Status", renderStatus(b.Status)},
			{"Access Key", accessKey},
		}))
		return nil
	},
}

var storageBucketDeleteCmd = &cobra.Command{
	Use:   "delete <name|id>",
	Short: "Delete a bucket (must be empty)",
	Long: "Delete a bucket. The bucket must be empty — a non-empty bucket is refused (409).\n\n" +
		"Emptying a bucket first (a --force that deletes objects) is part of the object\n" +
		"data-plane and ships with #266; for now empty it with an S3 client before deleting.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			ok, err := confirm(
				fmt.Sprintf("Delete bucket %q?", args[0]),
				"This action cannot be undone.",
				"Yes, delete")
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}

		c := getClient()
		id, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		if err := c.DeleteBucket(context.Background(), id); err != nil {
			var apiErr *client.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
				return fmt.Errorf("bucket %q is not empty: %s (empty it with an S3 client first; --force empty-then-delete lands in #266)", args[0], apiErr.Error())
			}
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Bucket %q deleted.", args[0]),
		))
		return nil
	},
}

var storageBucketCredentialsCmd = &cobra.Command{
	Use:     "get-credentials <name|id>",
	Aliases: []string{"credentials", "creds"},
	Short:   "Show a bucket's S3 connection details",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, _ := cmd.Flags().GetString("format")

		c := getClient()
		id, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		creds, err := c.GetBucketCredentials(context.Background(), id)
		if err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) || format == "json" {
			return renderData(creds)
		}

		if format == "env" {
			fmt.Printf("export AWS_ACCESS_KEY_ID=%s\n", creds.AccessKeyID)
			fmt.Printf("export AWS_ENDPOINT_URL_S3=%s\n", creds.Endpoint)
			fmt.Printf("export AWS_REGION=%s\n", creds.Region)
			// The S3 bucket name is the global alias, not the name you typed, and it
			// is the one value a caller cannot derive — without it every `aws s3`
			// command against this bucket answers NoSuchBucket (#559).
			fmt.Printf("export AWS_S3_BUCKET=%s\n", creds.Bucket)
			// The secret is never returned here: it exists only at the moment a key
			// is minted (#263). Say so on STDERR, because the documented use of this
			// format is `eval "$(...)"`, which captures stdout — a note printed there
			// is swallowed, and the reader debugs an opaque SigV4 failure instead of
			// reading the line that explained it (#559).
			fmt.Fprintln(os.Stderr, "note: AWS_SECRET_ACCESS_KEY was not set — this command never returns the secret.")
			fmt.Fprintln(os.Stderr, "      Mint one with: fpcloud storage keys create "+args[0]+" --read --write")
			return nil
		}

		pairs := [][]string{
			{"Bucket", creds.Bucket},
			{"Endpoint", creds.Endpoint},
			{"Region", creds.Region},
			{"Access Key", creds.AccessKeyID},
			{"Secret Key", mutedStyle.Render("not returned — mint a new key to obtain one")},
		}
		if creds.Note != "" {
			pairs = append(pairs, []string{"Note", mutedStyle.Render(creds.Note)})
		}
		fmt.Println(renderInfoBox("Bucket Credentials", pairs))
		return nil
	},
}

var storageBucketSetQuotaCmd = &cobra.Command{
	Use:   "set-quota <name|id>",
	Short: "Update a bucket's size / object-count quota (0 = unlimited)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("quota-size") && !cmd.Flags().Changed("quota-objects") {
			return fmt.Errorf("nothing to update: set --quota-size and/or --quota-objects")
		}
		c := getClient()
		id, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		existing, err := c.GetBucket(context.Background(), id)
		if err != nil {
			return err
		}
		maxSize := existing.QuotaMaxSize
		if cmd.Flags().Changed("quota-size") {
			sizeStr, _ := cmd.Flags().GetString("quota-size")
			maxSize, err = parseSize(sizeStr)
			if err != nil {
				return err
			}
		}
		maxObjects := existing.QuotaMaxObjects
		if cmd.Flags().Changed("quota-objects") {
			maxObjects, _ = cmd.Flags().GetInt64("quota-objects")
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		var b *client.Bucket
		var setErr error
		action := func() { b, setErr = c.SetBucketQuota(context.Background(), existing.ID, maxSize, maxObjects) }
		if !isStructured(outputFormat) {
			withSpinner("Updating quota...", action)
		} else {
			action()
		}
		if setErr != nil {
			return setErr
		}
		if isStructured(outputFormat) {
			return renderData(b)
		}
		fmt.Println(renderInfoBox("Quota Updated", [][]string{
			{"Name", b.Name},
			{"Quota", bucketQuota(b.QuotaMaxSize, b.QuotaMaxObjects)},
		}))
		return nil
	},
}

var storageBucketWebsiteCmd = &cobra.Command{
	Use:     "website",
	Aliases: []string{"web"},
	Short:   "Serve a bucket as a static website (Garage s3_web plane)",
}

var storageBucketWebsiteEnableCmd = &cobra.Command{
	Use:   "enable <bucket>",
	Short: "Serve a bucket as a public static website",
	Long: "Enable static-website serving for a bucket. The bucket is served over HTTP at\n" +
		"<bucket>.web.<platform-domain> with `index.html` as the default document.\n\n" +
		"This makes the bucket's objects world-readable over HTTP — anyone with the URL\n" +
		"can fetch them. Only enable it for content you intend to publish.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		index, _ := cmd.Flags().GetString("index")
		errorDoc, _ := cmd.Flags().GetString("error")
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			ok, err := confirm(
				fmt.Sprintf("Serve bucket %q as a public website?", args[0]),
				"Its objects become world-readable over HTTP — anyone with the URL can fetch them.",
				"Yes, publish")
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}

		c := getClient()
		id, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		var b *client.Bucket
		var setErr error
		action := func() {
			b, setErr = c.SetBucketWebsite(context.Background(), id, client.SetBucketWebsiteRequest{
				Enabled: true, IndexDocument: index, ErrorDocument: errorDoc,
			})
		}
		if !isStructured(outputFormat) {
			withSpinner("Enabling website...", action)
		} else {
			action()
		}
		if setErr != nil {
			return setErr
		}
		if isStructured(outputFormat) {
			return renderData(b)
		}
		fmt.Println(renderInfoBox("Website Enabled", websiteInfoPairs(b)))
		return nil
	},
}

var storageBucketWebsiteDisableCmd = &cobra.Command{
	Use:   "disable <bucket>",
	Short: "Stop serving a bucket as a static website",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		var b *client.Bucket
		var setErr error
		action := func() {
			b, setErr = c.SetBucketWebsite(context.Background(), id, client.SetBucketWebsiteRequest{Enabled: false})
		}
		if !isStructured(outputFormat) {
			withSpinner("Disabling website...", action)
		} else {
			action()
		}
		if setErr != nil {
			return setErr
		}
		if isStructured(outputFormat) {
			return renderData(b)
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Website serving disabled for %q.", args[0]),
		))
		return nil
	},
}

var storageBucketWebsiteShowCmd = &cobra.Command{
	Use:     "show <bucket>",
	Aliases: []string{"status", "get"},
	Short:   "Show a bucket's website serving status",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		b, err := c.GetBucket(context.Background(), id)
		if err != nil {
			return err
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(b)
		}
		fmt.Println(renderInfoBox("Bucket Website", websiteInfoPairs(b)))
		return nil
	},
}

var storageBucketBindCmd = &cobra.Command{
	Use:   "bind <bucket> --app <app>",
	Short: "Bind a bucket to an app (injects S3 credentials into its pods)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appRef, _ := cmd.Flags().GetString("app")
		if appRef == "" {
			return fmt.Errorf("--app is required")
		}
		readOnly, _ := cmd.Flags().GetBool("read-only")

		c := getClient()
		bucketID, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		appID, err := resolveAppID(c, appRef)
		if err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		var binding *client.AppBucketBinding
		var bindErr error
		action := func() { binding, bindErr = c.BindAppBucket(context.Background(), appID, bucketID, readOnly) }
		if !isStructured(outputFormat) {
			withSpinner("Binding bucket...", action)
		} else {
			action()
		}
		if bindErr != nil {
			return bindErr
		}
		if isStructured(outputFormat) {
			return renderData(binding)
		}
		fmt.Println(renderInfoBox("Bucket Bound", [][]string{
			{"Bucket", dashIfEmpty(binding.BucketName)},
			{"Endpoint", binding.Endpoint},
			{"Region", binding.Region},
			{"Read Only", yesNo(binding.ReadOnly)},
			{"Access Key", binding.AccessKeyID},
			{"Secret Name", binding.SecretName},
		}))
		return nil
	},
}

var storageBucketUnbindCmd = &cobra.Command{
	Use:   "unbind <bucket> --app <app>",
	Short: "Remove a bucket ⇄ app binding",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appRef, _ := cmd.Flags().GetString("app")
		if appRef == "" {
			return fmt.Errorf("--app is required")
		}
		c := getClient()
		bucketID, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		appID, err := resolveAppID(c, appRef)
		if err != nil {
			return err
		}
		var unbindErr error
		withSpinner("Unbinding bucket...", func() {
			unbindErr = c.UnbindAppBucket(context.Background(), appID, bucketID)
		})
		if unbindErr != nil {
			return unbindErr
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") + " Bucket unbound.",
		))
		return nil
	},
}

var storageBucketBindingsCmd = &cobra.Command{
	Use:     "bindings <app>",
	Aliases: []string{"list-bindings"},
	Short:   "List an app's bucket bindings",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		appID, err := resolveAppID(c, args[0])
		if err != nil {
			return err
		}
		bindings, err := c.ListAppBuckets(context.Background(), appID)
		if err != nil {
			return err
		}
		rows := make([][]string, len(bindings))
		for i, b := range bindings {
			rows[i] = []string{dashIfEmpty(b.BucketName), b.Endpoint, b.Region, yesNo(b.ReadOnly), b.AccessKeyID}
		}
		render([]string{"BUCKET", "ENDPOINT", "REGION", "READ ONLY", "ACCESS KEY"}, rows, bindings)
		return nil
	},
}

var storageKeysCreateCmd = &cobra.Command{
	Use:   "create <bucket>",
	Short: "Mint a scoped S3 access key for a bucket",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		read, _ := cmd.Flags().GetBool("read")
		write, _ := cmd.Flags().GetBool("write")
		owner, _ := cmd.Flags().GetBool("owner")

		c := getClient()
		bucketID, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		var key *client.BucketKey
		var createErr error
		action := func() {
			key, createErr = c.CreateBucketKey(context.Background(), bucketID, client.CreateBucketKeyRequest{
				Name:  name,
				Read:  read,
				Write: write,
				Owner: owner,
			})
		}
		if !isStructured(outputFormat) {
			withSpinner("Creating key...", action)
		} else {
			action()
		}
		if createErr != nil {
			return createErr
		}
		if isStructured(outputFormat) {
			return renderData(key)
		}
		fmt.Println(renderInfoBox("Access Key Created", [][]string{
			{"Access Key", key.AccessKeyID},
			{"Name", dashIfEmpty(key.Name)},
			{"Read", yesNo(key.CanRead)},
			{"Write", yesNo(key.CanWrite)},
			{"Owner", yesNo(key.CanOwner)},
			{"Secret Key", lipgloss.NewStyle().Bold(true).Foreground(colorInfo).Render(key.SecretAccessKey)},
			{"", mutedStyle.Render("Shown once — store it now; it is not retrievable later.")},
		}))
		return nil
	},
}

var storageKeysListCmd = &cobra.Command{
	Use:     "list <bucket>",
	Aliases: []string{"ls"},
	Short:   "List a bucket's scoped access keys",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		bucketID, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		keys, err := c.ListBucketKeys(context.Background(), bucketID)
		if err != nil {
			return err
		}
		rows := make([][]string, len(keys))
		for i, k := range keys {
			rows[i] = []string{k.AccessKeyID, dashIfEmpty(k.Name), yesNo(k.CanRead), yesNo(k.CanWrite), yesNo(k.CanOwner)}
		}
		render([]string{"ACCESS KEY", "NAME", "READ", "WRITE", "OWNER"}, rows, keys)
		return nil
	},
}

var storageKeysDeleteCmd = &cobra.Command{
	Use:   "delete <bucket> <access-key-id>",
	Short: "Revoke a scoped access key",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			ok, err := confirm(
				fmt.Sprintf("Revoke access key %q?", args[1]),
				"This action cannot be undone. Anything using it will lose access.",
				"Yes, revoke")
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		c := getClient()
		bucketID, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		if err := c.DeleteBucketKey(context.Background(), bucketID, args[1]); err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Access key %q revoked.", args[1]),
		))
		return nil
	},
}

// websiteInfoPairs renders a bucket's website-serving fields for detail views.
func websiteInfoPairs(b *client.Bucket) [][]string {
	pairs := [][]string{
		{"Name", b.Name},
		{"Website", yesNo(b.WebsiteEnabled)},
	}
	if b.WebsiteEnabled {
		url := b.WebsiteURL
		if url == "" {
			url = mutedStyle.Render("(served host not configured on this API)")
		} else {
			url = lipgloss.NewStyle().Bold(true).Foreground(colorInfo).Render(url)
		}
		index := b.WebsiteIndexDocument
		if index == "" {
			index = "index.html"
		}
		version := "not deployed yet"
		if b.WebsiteVersion > 0 {
			version = fmt.Sprintf("v%d", b.WebsiteVersion)
		}
		pairs = append(pairs,
			[]string{"URL", url},
			[]string{"Slug", dashIfEmpty(b.URLSlug)},
			[]string{"Index", index},
			[]string{"Error", dashIfEmpty(b.WebsiteErrorDocument)},
			[]string{"Deployed", version},
			[]string{"", mutedStyle.Render("Objects are world-readable over HTTP.")},
		)
	}
	return pairs
}

// bucketS3Name is the bucket's name in the object store's flat global
// namespace — what an `aws s3` command or a Terraform `backend "s3"` block has
// to name. Shown because it is NOT the name the user typed, and composing it by
// hand is the mistake it exists to prevent (buckets predating the current scheme
// kept an older alias).
func bucketS3Name(b *client.Bucket) string {
	if b.GlobalAlias == "" {
		return mutedStyle.Render("(assigned on provisioning)")
	}
	return b.GlobalAlias
}

// bucketUsage renders what the bucket holds against its quota, with the
// object count when the store reported one.
func bucketUsage(b *client.Bucket) string {
	out := usageAgainstQuota(b.UsedBytes, b.QuotaMaxSize)
	if b.ObjectCount != nil {
		out += fmt.Sprintf(" · %d objects", *b.ObjectCount)
	}
	return out
}

// usageAgainstQuota renders used bytes against a size quota, with the share
// of the quota consumed. A nil used count is the store not answering, shown
// as unknown rather than as empty; an unlimited quota has no share to show.
func usageAgainstQuota(used *int64, quota int64) string {
	if used == nil {
		return "— of " + humanizeQuota(quota)
	}
	out := humanizeSize(*used) + " of " + humanizeQuota(quota)
	if quota > 0 {
		out += " (" + usagePercent(*used, quota) + ")"
	}
	return out
}

// usagePercent rounds the share to a whole percent, except that anything
// above zero and below one percent is "<1%" rather than a "0%" that reads
// as empty.
func usagePercent(used, quota int64) string {
	pct := float64(used) * 100 / float64(quota)
	if used > 0 && pct < 1 {
		return "<1%"
	}
	return fmt.Sprintf("%.0f%%", pct)
}

// bucketQuota renders the size/object quota pair for detail views.

func bucketQuota(maxSize, maxObjects int64) string {
	objects := "unlimited"
	if maxObjects > 0 {
		objects = strconv.FormatInt(maxObjects, 10) + " objects"
	}
	return humanizeQuota(maxSize) + " / " + objects
}

func init() {
	storageBucketCreateCmd.Flags().String("quota-size", "", "Max total size (bytes or Ki/Mi/Gi/Ti); unset = the platform default, 0 = unlimited (operator-only)")
	storageBucketCreateCmd.Flags().Int64("quota-objects", 0, "Max object count; unset = the platform default, 0 = unlimited (operator-only)")

	storageBucketDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	storageBucketCredentialsCmd.Flags().String("format", "table", "Output form: table | env | json")

	storageBucketSetQuotaCmd.Flags().String("quota-size", "", "Max total size (bytes or Ki/Mi/Gi/Ti); 0 = unlimited (operator-only)")
	storageBucketSetQuotaCmd.Flags().Int64("quota-objects", 0, "Max object count; 0 = unlimited (operator-only)")

	storageBucketWebsiteEnableCmd.Flags().String("index", "", "Index document served for a directory request (default index.html)")
	storageBucketWebsiteEnableCmd.Flags().String("error", "", "Document served on a miss (optional)")
	storageBucketWebsiteEnableCmd.Flags().BoolP("yes", "y", false, "Skip the public-read confirmation prompt")
	storageBucketWebsiteCmd.AddCommand(storageBucketWebsiteEnableCmd, storageBucketWebsiteDisableCmd, storageBucketWebsiteShowCmd)

	storageBucketLifecycleSetCmd.Flags().String("prefix", "", "Key prefix the rule covers (default: the whole bucket)")
	storageBucketLifecycleSetCmd.Flags().Int("expire-days", 0, "Delete objects older than N days")
	storageBucketLifecycleSetCmd.Flags().Int("abort-incomplete-days", 0, "Abort multipart uploads left incomplete for N days")
	storageBucketLifecycleDeleteCmd.Flags().String("prefix", "", "Key prefix whose rule to remove (default: the whole-bucket rule)")
	storageBucketLifecycleDeleteCmd.Flags().Bool("all", false, "Remove every expiry rule on the bucket")
	storageBucketLifecycleCmd.AddCommand(storageBucketLifecycleListCmd, storageBucketLifecycleSetCmd, storageBucketLifecycleDeleteCmd)

	storageBucketCORSSetCmd.Flags().StringArray("origin", nil, "Origin allowed to call the bucket from a browser, e.g. https://example.com (repeatable, * for any)")
	storageBucketCORSSetCmd.Flags().StringArray("method", nil, "HTTP method the origin may use (repeatable): GET, PUT, HEAD, POST, DELETE")
	storageBucketCORSSetCmd.Flags().StringArray("header", nil, "Request header the browser may send (repeatable, * for any)")
	storageBucketCORSSetCmd.Flags().StringArray("expose-header", nil, "Response header the page may read (repeatable) — an upload needs ETag")
	storageBucketCORSSetCmd.Flags().Int("max-age", 0, "Seconds the browser may cache the preflight (0 = its own default)")
	storageBucketCORSSetCmd.Flags().String("from-file", "", "JSON file holding the whole rule list, for configurations one rule cannot express")
	storageBucketCORSCmd.AddCommand(storageBucketCORSListCmd, storageBucketCORSSetCmd, storageBucketCORSClearCmd)

	storageBucketBindCmd.Flags().String("app", "", "App to bind the bucket to (name or id, required)")
	storageBucketBindCmd.Flags().Bool("read-only", false, "Bind with a read-only scoped key")

	storageBucketUnbindCmd.Flags().String("app", "", "App to unbind the bucket from (name or id, required)")

	storageKeysCreateCmd.Flags().String("name", "", "Optional label for the key")
	storageKeysCreateCmd.Flags().Bool("read", false, "Grant read (GetObject/ListBucket)")
	storageKeysCreateCmd.Flags().Bool("write", false, "Grant write (PutObject/DeleteObject)")
	storageKeysCreateCmd.Flags().Bool("owner", false, "Grant owner (bucket-admin) permissions")

	storageKeysDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	storageBucketCmd.AddCommand(
		storageBucketCreateCmd,
		storageBucketListCmd,
		storageBucketDescribeCmd,
		storageBucketDeleteCmd,
		storageBucketCredentialsCmd,
		storageBucketSetQuotaCmd,
		storageBucketWebsiteCmd,
		storageBucketLifecycleCmd,
		storageBucketCORSCmd,
		storageBucketBindCmd,
		storageBucketUnbindCmd,
		storageBucketBindingsCmd,
	)
	storageKeysCmd.AddCommand(storageKeysCreateCmd, storageKeysListCmd, storageKeysDeleteCmd)
	storageCmd.AddCommand(storageBucketCmd, storageKeysCmd)
	rootCmd.AddCommand(storageCmd)
}

var storageBucketLifecycleCmd = &cobra.Command{
	Use:   "lifecycle",
	Short: "Expire objects on a bucket by age (object lifecycle rules)",
	Long: "Rules are keyed by prefix, so one bucket can expire derived artefacts under\n" +
		"`renders/` while user uploads alongside them are kept forever.\n\n" +
		"Expiry is by AGE, not by whether anything still references the object: an\n" +
		"object old enough is deleted even if it is current. Only use this for content\n" +
		"you can regenerate — it is a cache eviction policy, not a backup retention one.",
}

var storageBucketLifecycleListCmd = &cobra.Command{
	Use:     "list <bucket>",
	Aliases: []string{"ls"},
	Short:   "List a bucket's expiry rules",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		bucketID, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		rules, err := c.ListBucketLifecycleRules(context.Background(), bucketID)
		if err != nil {
			return err
		}
		rows := make([][]string, len(rules))
		for i, r := range rules {
			rows[i] = []string{lifecyclePrefixLabel(r.Prefix), lifecycleDays(r.ExpireDays), lifecycleDays(r.AbortIncompleteUploadDays)}
		}
		render([]string{"PREFIX", "EXPIRE AFTER", "ABORT UPLOADS AFTER"}, rows, rules)
		return nil
	},
}

var storageBucketLifecycleSetCmd = &cobra.Command{
	Use:   "set <bucket>",
	Short: "Set the expiry rule for one prefix",
	Long: "Set the expiry rule for one prefix of a bucket. Objects under the prefix are\n" +
		"deleted once they are --expire-days old, measured from when each object was\n" +
		"written — regardless of whether it is still the current version of something.\n" +
		"--abort-incomplete-days reclaims the parts of multipart uploads that were never\n" +
		"completed. Without --prefix the rule covers the whole bucket.\n\n" +
		"Setting a prefix again replaces that prefix's rule and leaves every other rule\n" +
		"untouched. The prefix is the rule's blast radius, not a label on it: changing it\n" +
		"retires one rule and writes another over whatever the new prefix matches, so\n" +
		"widening renders/ to nothing expires the entire bucket at that age, uploads\n" +
		"included.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prefix, _ := cmd.Flags().GetString("prefix")
		expireDays, _ := cmd.Flags().GetInt("expire-days")
		abortDays, _ := cmd.Flags().GetInt("abort-incomplete-days")
		if expireDays <= 0 && abortDays <= 0 {
			return fmt.Errorf("nothing to set: pass --expire-days and/or --abort-incomplete-days")
		}
		c := getClient()
		bucketID, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		rule, err := c.SetBucketLifecycleRule(context.Background(), bucketID, client.SetBucketLifecycleRuleRequest{
			Prefix: prefix, ExpireDays: expireDays, AbortIncompleteUploadDays: abortDays,
		})
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(rule)
		}
		fmt.Println(renderInfoBox("Lifecycle Rule Set", [][]string{
			{"Prefix", lifecyclePrefixLabel(rule.Prefix)},
			{"Expire after", lifecycleDays(rule.ExpireDays)},
			{"Abort uploads after", lifecycleDays(rule.AbortIncompleteUploadDays)},
			{"", mutedStyle.Render("Objects are deleted by age, whether or not they are still current.")},
		}))
		return nil
	},
}

var storageBucketLifecycleDeleteCmd = &cobra.Command{
	Use:     "delete <bucket>",
	Aliases: []string{"rm"},
	Short:   "Remove one prefix's expiry rule, or all of them with --all",
	Long: "Remove the expiry rule for --prefix; objects under it stop expiring. Without\n" +
		"--prefix this removes the whole-bucket rule (the one set without a prefix) — to\n" +
		"drop every rule on the bucket, pass --all.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prefix, _ := cmd.Flags().GetString("prefix")
		all, _ := cmd.Flags().GetBool("all")
		if all && cmd.Flags().Changed("prefix") {
			return fmt.Errorf("--all removes every rule; drop --prefix or drop --all")
		}
		c := getClient()
		bucketID, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		if all {
			if err := c.ClearBucketLifecycleRules(context.Background(), bucketID); err != nil {
				return err
			}
			fmt.Println(successBox.Render(
				lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
					fmt.Sprintf(" All expiry rules on %q removed. Nothing on the bucket expires now.", args[0]),
			))
			return nil
		}
		if err := c.DeleteBucketLifecycleRule(context.Background(), bucketID, prefix); err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Expiry rule for %s removed.", lifecyclePrefixLabel(prefix)),
		))
		return nil
	},
}

var storageBucketCORSCmd = &cobra.Command{
	Use:   "cors",
	Short: "Let a browser call the bucket directly (cross-origin rules)",
	Long: "A page on your own domain cannot upload to or read from a bucket until the\n" +
		"bucket says its origin is allowed. The browser asks first, with an OPTIONS\n" +
		"preflight, and refuses the real request if the answer does not name the origin.\n\n" +
		"A presigned url does not remove the need for this. The preflight carries no\n" +
		"signature, so it is answered before any credential is looked at — a correctly\n" +
		"signed upload still fails without a rule here, and the browser reports it as a\n" +
		"CORS error rather than as anything about permissions.\n\n" +
		"You only need this when the browser talks to the bucket. An app that uploads\n" +
		"through its own backend never sends a preflight and needs no rule.",
}

var storageBucketCORSListCmd = &cobra.Command{
	Use:     "list <bucket>",
	Aliases: []string{"ls"},
	Short:   "List a bucket's cross-origin rules",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		bucketID, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		rules, err := c.ListBucketCORSRules(context.Background(), bucketID)
		if err != nil {
			return err
		}
		rows := make([][]string, len(rules))
		for i, r := range rules {
			rows[i] = []string{
				strings.Join(r.AllowedOrigins, ", "),
				strings.Join(r.AllowedMethods, ", "),
				corsHeaderList(r.AllowedHeaders),
				corsHeaderList(r.ExposeHeaders),
				corsMaxAge(r.MaxAgeSeconds),
			}
		}
		render([]string{"ORIGINS", "METHODS", "HEADERS", "EXPOSES", "MAX AGE"}, rows, rules)
		return nil
	},
}

var storageBucketCORSSetCmd = &cobra.Command{
	Use:   "set <bucket>",
	Short: "Replace a bucket's cross-origin rules",
	Long: "Declare which browser origins may call the bucket, and how.\n\n" +
		"This replaces the WHOLE configuration — there is no way to address one rule,\n" +
		"because a rule has no name and two rules can differ only in their order. What\n" +
		"you pass is what the bucket will have.\n\n" +
		"The flags describe one rule, which is what almost every site needs:\n\n" +
		"  fpcloud storage bucket cors set photos \\\n" +
		"      --origin https://example.com --method GET --method PUT \\\n" +
		"      --header '*' --expose-header ETag --max-age 3600\n\n" +
		"--expose-header is easy to leave out and hard to diagnose: without ETag the\n" +
		"upload succeeds and the page cannot read what it uploaded.\n\n" +
		"For anything one rule cannot express — different methods per origin — pass\n" +
		"--from-file with a JSON list of rules, the same shape `list -o json` prints.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fromFile, _ := cmd.Flags().GetString("from-file")
		origins, _ := cmd.Flags().GetStringArray("origin")
		methods, _ := cmd.Flags().GetStringArray("method")
		headers, _ := cmd.Flags().GetStringArray("header")
		exposes, _ := cmd.Flags().GetStringArray("expose-header")
		maxAge, _ := cmd.Flags().GetInt("max-age")

		var rules []client.BucketCORSRuleRequest
		switch {
		case fromFile != "" && len(origins) > 0:
			return fmt.Errorf("--from-file already carries the whole configuration; drop the per-rule flags")
		case fromFile != "":
			raw, err := os.ReadFile(fromFile)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &rules); err != nil {
				return fmt.Errorf("parse %s: %w", fromFile, err)
			}
		case len(origins) == 0:
			return fmt.Errorf("nothing to set: pass --origin (and --method), or --from-file; to remove every rule use `cors clear`")
		default:
			rules = []client.BucketCORSRuleRequest{{
				AllowedOrigins: origins,
				AllowedMethods: methods,
				AllowedHeaders: headers,
				ExposeHeaders:  exposes,
				MaxAgeSeconds:  maxAge,
			}}
		}

		c := getClient()
		bucketID, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		written, err := c.SetBucketCORSRules(context.Background(), bucketID, client.SetBucketCORSRequest{Rules: rules})
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(written)
		}
		rows := [][]string{}
		for _, r := range written {
			rows = append(rows,
				[]string{"Origins", strings.Join(r.AllowedOrigins, ", ")},
				[]string{"Methods", strings.Join(r.AllowedMethods, ", ")},
				[]string{"Exposes", corsHeaderList(r.ExposeHeaders)},
				[]string{"Max age", corsMaxAge(r.MaxAgeSeconds)},
			)
		}
		rows = append(rows, []string{"", mutedStyle.Render("This is now the bucket's entire cross-origin configuration.")})
		fmt.Println(renderInfoBox("CORS Rules Set", rows))
		return nil
	},
}

var storageBucketCORSClearCmd = &cobra.Command{
	Use:   "clear <bucket>",
	Short: "Remove every cross-origin rule",
	Long: "Removes the whole configuration. No browser origin can reach the bucket\n" +
		"afterwards; anything reaching it from a server is unaffected.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		bucketID, err := resolveBucketID(c, args[0])
		if err != nil {
			return err
		}
		if err := c.ClearBucketCORSRules(context.Background(), bucketID); err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" All cross-origin rules on %q removed. No browser origin reaches it now.", args[0]),
		))
		return nil
	},
}

// corsHeaderList renders an optional header list; empty reads as nothing set
// rather than as an empty set someone declared.
func corsHeaderList(h []string) string {
	if len(h) == 0 {
		return "—"
	}
	return strings.Join(h, ", ")
}

// corsMaxAge renders the preflight cache lifetime, with 0 left to the browser.
func corsMaxAge(secs int) string {
	if secs <= 0 {
		return "—"
	}
	return fmt.Sprintf("%ds", secs)
}

// lifecyclePrefixLabel names the scope a rule covers; the empty prefix is the
// whole bucket, which reads as nothing at all in a table.
func lifecyclePrefixLabel(prefix string) string {
	if prefix == "" {
		return "(whole bucket)"
	}
	return prefix
}

// lifecycleDays renders a day count, with 0 meaning the rule is not set.
func lifecycleDays(days int) string {
	if days <= 0 {
		return "—"
	}
	return strconv.Itoa(days) + "d"
}
