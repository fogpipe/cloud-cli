package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

// storage_objects.go is the data-plane object CLI (#266): an `aws s3` /
// `gcloud storage`-shaped surface (ls/cp/mv/rm/sync/cat/du/presign) that talks
// S3 SigV4 straight to the bucket's Garage endpoint with a scoped key — the API
// never proxies object bytes (ADR-039). Garage has no ACLs, SSE, storage
// classes, or versioning, so those flags are intentionally absent.

// --- URL scheme -------------------------------------------------------------

// parseObjectURL splits an argument into a bucket/key when it carries the
// branded fps:// scheme (or the s3:// alias); bare paths are local. It returns
// isRemote=false for local paths, leaving bucket/key empty.
func parseObjectURL(s string) (bucket, key string, isRemote bool) {
	for _, scheme := range []string{"fps://", "s3://"} {
		if rest, ok := strings.CutPrefix(s, scheme); ok {
			b, k, _ := strings.Cut(rest, "/")
			return b, k, true
		}
	}
	return "", "", false
}

// --- exclude/include filtering ----------------------------------------------

// filterRule is one --exclude/--include glob, in command-line order.
type filterRule struct {
	include bool
	pattern string
}

// includedByFilters reports whether a path survives the exclude/include rules.
// Everything is included by default; rules apply left-to-right and the last
// matching rule wins (matching `aws s3` precedence).
func includedByFilters(rules []filterRule, p string) bool {
	included := true
	for _, r := range rules {
		if ok, _ := filepath.Match(r.pattern, p); ok {
			included = r.include
		}
		// aws also matches the basename; mirror that so `*.log` works on keys.
		if ok, _ := filepath.Match(r.pattern, path.Base(p)); ok {
			included = r.include
		}
	}
	return included
}

// globFilter is a pflag.Value that appends rules to a shared slice so --exclude
// and --include preserve their interleaved command-line order.
type globFilter struct {
	rules   *[]filterRule
	include bool
}

func (g globFilter) String() string { return "" }
func (g globFilter) Type() string   { return "glob" }
func (g globFilter) Set(v string) error {
	*g.rules = append(*g.rules, filterRule{include: g.include, pattern: v})
	return nil
}

// addFilterFlags wires --exclude/--include onto cmd, returning the ordered rule
// slice they populate during parse.
func addFilterFlags(cmd *cobra.Command) *[]filterRule {
	rules := &[]filterRule{}
	cmd.Flags().Var(globFilter{rules, false}, "exclude", "Exclude keys/paths matching a glob (repeatable, later wins)")
	cmd.Flags().Var(globFilter{rules, true}, "include", "Re-include keys/paths matching a glob (repeatable, later wins)")
	return rules
}

// --- credential acquisition + caching ---------------------------------------

// cachedS3Creds is the on-disk session credential + endpoint for a bucket,
// stored under <stateDir>/storage/<bucketID>.json (mode 0600). It expires, so
// what is cached here is worth little for long — and the file is the only place
// the secret ever lands.
type cachedS3Creds struct {
	AccessKeyID     string    `json:"access_key_id"`
	SecretAccessKey string    `json:"secret_access_key"`
	Endpoint        string    `json:"endpoint"`
	Region          string    `json:"region"`
	BucketName      string    `json:"bucket_name"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// credsExpiryMargin is how long a cached credential must still be good for to be
// reused. A transfer started against a credential expiring in seconds fails
// mid-flight instead of at the first call, so the margin buys the whole command
// rather than its first request.
const credsExpiryMargin = 2 * time.Minute

func storageCredsDir() string {
	return filepath.Join(stateDir(), "storage")
}

func storageCredsPath(bucketID string) string {
	return filepath.Join(storageCredsDir(), bucketID+".json")
}

func loadCachedS3Creds(bucketID string) (*cachedS3Creds, bool) {
	data, err := os.ReadFile(storageCredsPath(bucketID))
	if err != nil {
		return nil, false
	}
	var creds cachedS3Creds
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, false
	}
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" || creds.Endpoint == "" || creds.BucketName == "" {
		return nil, false
	}
	if time.Now().Add(credsExpiryMargin).After(creds.ExpiresAt) {
		return nil, false
	}
	return &creds, true
}

func saveCachedS3Creds(bucketID string, creds *cachedS3Creds) error {
	if err := os.MkdirAll(storageCredsDir(), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	return os.WriteFile(storageCredsPath(bucketID), data, 0o600)
}

// mintS3Creds mints a fresh expiring session credential for the bucket and
// caches it until it expires.
//
// It asks for no particular permissions: the credential carries whatever the
// caller may already do to the bucket's objects (ADR-074), so a read-only member
// gets a read-only credential rather than a refusal, and shipping a site needs
// no more than the role that may change what it serves.
func mintS3Creds(ctx context.Context, c *client.Client, bucketID string) (*cachedS3Creds, error) {
	session, err := c.MintBucketSessionCredentials(ctx, bucketID)
	if err != nil {
		return nil, fmt.Errorf("mint session credential: %w", err)
	}
	creds := &cachedS3Creds{
		AccessKeyID:     session.AccessKeyID,
		SecretAccessKey: session.SecretAccessKey,
		Endpoint:        session.Endpoint,
		Region:          session.Region,
		BucketName:      session.Bucket,
		ExpiresAt:       session.ExpiresAt,
	}
	if creds.BucketName == "" {
		if b, err := c.GetBucket(ctx, bucketID); err == nil {
			creds.BucketName = b.Name
			if creds.Endpoint == "" {
				creds.Endpoint = b.Endpoint
			}
			if creds.Region == "" {
				creds.Region = b.Region
			}
		}
	}
	if creds.Endpoint == "" {
		return nil, fmt.Errorf("bucket has no S3 endpoint configured (object storage not provisioned yet)")
	}
	_ = saveCachedS3Creds(bucketID, creds)
	return creds, nil
}

// remote bundles an S3 client with the resolved bucket name for a command.
type remote struct {
	cli    *s3.Client
	bucket string
}

// bucketS3Client resolves bucketRef to a bucket id, reuses cached creds (or
// mints fresh ones), and builds a path-style SigV4 s3.Client pointed at Garage.
// forceRefresh drops the cache first so an auth failure can re-mint.
func bucketS3Client(ctx context.Context, c *client.Client, bucketRef string, forceRefresh bool) (*s3.Client, string, error) {
	bucketID, err := resolveBucketID(c, bucketRef)
	if err != nil {
		return nil, "", err
	}
	if forceRefresh {
		_ = os.Remove(storageCredsPath(bucketID))
	}
	creds, ok := loadCachedS3Creds(bucketID)
	if !ok {
		creds, err = mintS3Creds(ctx, c, bucketID)
		if err != nil {
			return nil, "", err
		}
	}
	region := creds.Region
	if region == "" {
		region = "garage"
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(creds.AccessKeyID, creds.SecretAccessKey, "")),
		// Garage rejects the SDK's default trailing/flexible checksums; only
		// send/validate them when an operation strictly requires it.
		awsconfig.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
		awsconfig.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
	)
	if err != nil {
		return nil, "", err
	}
	endpoint := creds.Endpoint
	cli := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = &endpoint
		o.UsePathStyle = true // Garage is path-style
	})
	return cli, creds.BucketName, nil
}

// isS3AuthError reports whether err is an S3 authentication/authorization
// failure that a fresh scoped key might fix.
func isS3AuthError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "InvalidAccessKeyId", "SignatureDoesNotMatch", "AccessDenied", "Forbidden", "ExpiredToken":
			return true
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "status code: 403") ||
		strings.Contains(msg, "InvalidAccessKeyId") ||
		strings.Contains(msg, "SignatureDoesNotMatch")
}

// retryOnAuth runs fn(false); on an S3 auth error it re-runs fn(true) once so
// the command rebuilds its clients with freshly minted creds.
func retryOnAuth(fn func(forceRefresh bool) error) error {
	err := fn(false)
	if isS3AuthError(err) {
		return fn(true)
	}
	return err
}

// --- shared helpers ---------------------------------------------------------

func verbose() bool {
	return !isStructured(rootCmd.Flag("output").Value.String())
}

// joinKey concatenates an S3 key prefix with a relative path, collapsing the
// boundary slash.
func joinKey(prefix, rel string) string {
	rel = strings.TrimPrefix(rel, "/")
	if prefix == "" {
		return rel
	}
	return strings.TrimSuffix(prefix, "/") + "/" + rel
}

// webContentTypes pins the Content-Type for web-critical extensions so a
// static site serves correctly regardless of the host's MIME table (Go's
// mime.TypeByExtension reads the OS table, which varies per machine and often
// lacks js/wasm/woff2).
var webContentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".htm":   "text/html; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".json":  "application/json",
	".map":   "application/json",
	".svg":   "image/svg+xml",
	".xml":   "application/xml",
	".txt":   "text/plain; charset=utf-8",
	".ico":   "image/x-icon",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".avif":  "image/avif",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".wasm":  "application/wasm",
	".pdf":   "application/pdf",
}

// contentTypeFor infers an object's Content-Type from its key extension,
// preferring the pinned web table and falling back to the OS MIME table.
// Returns "" when unknown, leaving the store to apply its own default.
func contentTypeFor(key string) string {
	ext := strings.ToLower(path.Ext(key))
	if ct, ok := webContentTypes[ext]; ok {
		return ct
	}
	return mime.TypeByExtension(ext)
}

// Cache-Control values for a static site. The split is the standard one: a
// fingerprinted asset can be cached forever because its name changes whenever
// its bytes do, while an entry document must be revalidated or a browser will
// keep serving the old one — which, with versioned deploys (ADR-050), means an
// index that references assets the live version no longer has.
const (
	immutableCacheControl  = "public, max-age=31536000, immutable"
	revalidateCacheControl = "no-cache"
	defaultCacheControl    = "public, max-age=3600"
)

// cacheControlFlagHelp is shared by every command that uploads objects.
const cacheControlFlagHelp = "Cache-Control for matching objects, \"<glob>=<value>\" " +
	"(e.g. \"*.html=no-cache\"); repeatable, first match wins"

// cacheRule is one --cache-control glob=value override.
type cacheRule struct {
	pattern string
	value   string
}

// looksLikeHash reports whether a filename segment is a build fingerprint.
// Deliberately strict — it requires length and a digit, so ordinary words are
// not mistaken for hashes. Erring this way costs a false negative (an asset
// cached for an hour instead of a year); the opposite error would pin a mutable
// file in browser caches for a year, which nothing can undo.
func looksLikeHash(s string) bool {
	if len(s) < 8 {
		return false
	}
	digit := false
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
		default:
			return false
		}
	}
	return digit
}

// isFingerprinted reports whether a filename carries a build hash, covering both
// conventions in the wild: Vite's "index-DfGh1a2b.js" and webpack's
// "main.a1b2c3d4.js".
func isFingerprinted(base string) bool {
	ext := path.Ext(base)
	if ext == "" {
		return false
	}
	stem := strings.TrimSuffix(base, ext)
	i := strings.LastIndexAny(stem, ".-")
	if i < 0 {
		return false
	}
	return looksLikeHash(stem[i+1:])
}

// webCacheControlFor is the default cache policy for an object in a static site.
// Directory conventions (assets/, _next/static/) are deliberately NOT treated as
// immutable on their own: build tools put unhashed files there too, and a
// year-long cache on one of those is unrecoverable.
func webCacheControlFor(key string) string {
	base := path.Base(key)
	switch strings.ToLower(path.Ext(base)) {
	case ".html", ".htm":
		return revalidateCacheControl
	}
	if isFingerprinted(base) {
		return immutableCacheControl
	}
	return defaultCacheControl
}

// putOpts is the per-object metadata a transfer applies on upload.
type putOpts struct {
	contentType string      // forced Content-Type; empty infers from the key
	cacheRules  []cacheRule // --cache-control overrides, first match wins
	webCache    bool        // apply the static-site Cache-Control defaults
}

// cacheControlFor resolves an object's Cache-Control: an explicit override wins,
// then the web defaults when enabled, otherwise nothing is set (a plain
// `storage cp` of a data object should not acquire a web cache policy).
func (o putOpts) cacheControlFor(key string) string {
	base := path.Base(key)
	for _, r := range o.cacheRules {
		if ok, _ := path.Match(r.pattern, key); ok {
			return r.value
		}
		if ok, _ := path.Match(r.pattern, base); ok {
			return r.value
		}
	}
	if o.webCache {
		return webCacheControlFor(key)
	}
	return ""
}

// parseCacheControlFlags reads repeated --cache-control "glob=value" pairs.
func parseCacheControlFlags(values []string) ([]cacheRule, error) {
	rules := make([]cacheRule, 0, len(values))
	for _, raw := range values {
		pattern, value, ok := strings.Cut(raw, "=")
		pattern, value = strings.TrimSpace(pattern), strings.TrimSpace(value)
		if !ok || pattern == "" || value == "" {
			return nil, fmt.Errorf("invalid --cache-control %q (expected \"<glob>=<value>\", e.g. \"*.html=no-cache\")", raw)
		}
		rules = append(rules, cacheRule{pattern: pattern, value: value})
	}
	return rules, nil
}

// fmtSize renders a byte count, humanized when human is set.
func fmtSize(n int64, human bool) string {
	if !human {
		return strconv.FormatInt(n, 10)
	}
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	units := []struct {
		suffix string
		factor int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}}
	for _, u := range units {
		if n >= u.factor {
			return strings.TrimSuffix(strconv.FormatFloat(float64(n)/float64(u.factor), 'f', 1, 64), ".0") + " " + u.suffix
		}
	}
	return strconv.FormatInt(n, 10) + " B"
}

// objectEntry is a listed object (for -o json).
type objectEntry struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"last_modified"`
}

// listObjects walks every page of ListObjectsV2 under prefix, invoking fn per
// object. delimiter "" recurses; "/" stops at the first level.
func listObjects(ctx context.Context, cli *s3.Client, bucket, prefix, delimiter string, fn func(s3types.Object) error, dirFn func(string) error) error {
	in := &s3.ListObjectsV2Input{Bucket: &bucket}
	if prefix != "" {
		in.Prefix = &prefix
	}
	if delimiter != "" {
		in.Delimiter = &delimiter
	}
	p := s3.NewListObjectsV2Paginator(cli, in)
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return err
		}
		if dirFn != nil {
			for _, cp := range page.CommonPrefixes {
				if err := dirFn(aws.ToString(cp.Prefix)); err != nil {
					return err
				}
			}
		}
		for _, obj := range page.Contents {
			if err := fn(obj); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- ls ---------------------------------------------------------------------

var storageLsCmd = &cobra.Command{
	Use:   "ls [fps://bucket[/prefix]]",
	Short: "List objects in a bucket (ListObjectsV2)",
	Long: "List objects under a bucket prefix.\n\n" +
		"Without --recursive, keys are grouped at the next '/' (folder view).\n" +
		"Garage has no ACLs/versioning, so those aws flags are unsupported.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("specify a bucket: fpcloud storage ls fps://<bucket>[/prefix]")
		}
		bucket, prefix, isRemote := parseObjectURL(args[0])
		if !isRemote {
			return fmt.Errorf("ls takes a remote URL: fps://<bucket>[/prefix]")
		}
		recursive, _ := cmd.Flags().GetBool("recursive")
		human, _ := cmd.Flags().GetBool("human-readable")
		summarize, _ := cmd.Flags().GetBool("summarize")

		ctx := context.Background()
		c := getClient()
		delimiter := "/"
		if recursive {
			delimiter = ""
		}

		var entries []objectEntry
		var dirs []string
		var totalSize, totalCount int64
		err := retryOnAuth(func(force bool) error {
			entries, dirs, totalSize, totalCount = nil, nil, 0, 0
			cli, s3bucket, err := bucketS3Client(ctx, c, bucket, force)
			if err != nil {
				return err
			}
			return listObjects(ctx, cli, s3bucket, prefix, delimiter,
				func(o s3types.Object) error {
					entries = append(entries, objectEntry{Key: aws.ToString(o.Key), Size: aws.ToInt64(o.Size), LastModified: aws.ToTime(o.LastModified)})
					totalSize += aws.ToInt64(o.Size)
					totalCount++
					return nil
				},
				func(d string) error { dirs = append(dirs, d); return nil })
		})
		if err != nil {
			return err
		}

		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(map[string]any{"prefixes": dirs, "objects": entries, "total_size": totalSize, "total_objects": totalCount})
		}
		for _, d := range dirs {
			fmt.Printf("%31s %s\n", "PRE", d)
		}
		for _, e := range entries {
			fmt.Printf("%s %10s %s\n", e.LastModified.Format("2006-01-02 15:04:05"), fmtSize(e.Size, human), e.Key)
		}
		if summarize {
			fmt.Println()
			fmt.Printf("Total Objects: %d\n", totalCount)
			fmt.Printf("Total Size: %s\n", fmtSize(totalSize, human))
		}
		return nil
	},
}

// --- transfer engine (cp/mv) ------------------------------------------------

// putInput builds a PutObjectInput, attaching Content-Type and Cache-Control
// only when known so an unset value leaves the store's default untouched.
func putInput(bucket, key string, body io.Reader, ct, cc string) *s3.PutObjectInput {
	in := &s3.PutObjectInput{Bucket: &bucket, Key: &key, Body: body}
	if ct != "" {
		in.ContentType = &ct
	}
	if cc != "" {
		in.CacheControl = &cc
	}
	return in
}

// copyOne performs a single object copy across the four direction combinations.
// ctOverride forces the uploaded object's Content-Type; when empty it is
// inferred from the destination key extension (and, for remote->remote, from
// the source object's own type first) so static sites serve the right MIME.
func copyOne(ctx context.Context, srcR *remote, srcLocal string, dstR *remote, dstLocal string, opts putOpts) error {
	switch {
	case srcR != nil && dstR != nil: // remote -> remote (stream through)
		out, err := srcR.cli.GetObject(ctx, &s3.GetObjectInput{Bucket: &srcR.bucket, Key: &srcLocal})
		if err != nil {
			return err
		}
		defer out.Body.Close()
		ct := opts.contentType
		if ct == "" {
			ct = aws.ToString(out.ContentType)
		}
		if ct == "" {
			ct = contentTypeFor(dstLocal)
		}
		_, err = manager.NewUploader(dstR.cli).Upload(ctx, putInput(dstR.bucket, dstLocal, out.Body, ct, opts.cacheControlFor(dstLocal)))
		return err
	case srcLocal != "" && dstR != nil: // local -> remote (upload)
		ct := opts.contentType
		if ct == "" {
			ct = contentTypeFor(dstLocal)
		}
		if srcLocal == "-" {
			// os.Stdin is an *os.File (io.Seeker), so the uploader tries to Seek it
			// to size the body — which fails with "illegal seek" on a pipe. Hide the
			// Seeker behind a plain io.Reader so the uploader streams stdin in parts.
			_, err := manager.NewUploader(dstR.cli).Upload(ctx, putInput(dstR.bucket, dstLocal, struct{ io.Reader }{os.Stdin}, ct, opts.cacheControlFor(dstLocal)))
			return err
		}
		f, err := os.Open(srcLocal)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = manager.NewUploader(dstR.cli).Upload(ctx, putInput(dstR.bucket, dstLocal, f, ct, opts.cacheControlFor(dstLocal)))
		return err
	case srcR != nil && dstLocal != "": // remote -> local (download)
		out, err := srcR.cli.GetObject(ctx, &s3.GetObjectInput{Bucket: &srcR.bucket, Key: &srcLocal})
		if err != nil {
			return err
		}
		defer out.Body.Close()
		if dstLocal == "-" {
			_, err := io.Copy(os.Stdout, out.Body)
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dstLocal), 0o755); err != nil {
			return err
		}
		f, err := os.Create(dstLocal)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, out.Body); err != nil {
			f.Close()
			return err
		}
		// Close reports the write errors a buffered copy hasn't surfaced yet —
		// dropping it lets a truncated download exit 0.
		return f.Close()
	}
	return fmt.Errorf("unsupported copy direction (local-to-local not supported; use cp)")
}

// resolveDstKey picks the destination object key for a single-file upload:
// an empty or trailing-slash key gets the source basename appended.
func resolveDstKey(dstKey, srcName string) string {
	if dstKey == "" || strings.HasSuffix(dstKey, "/") {
		return joinKey(dstKey, filepath.Base(srcName))
	}
	return dstKey
}

// resolveDstPath picks the local destination for a single-file download: an
// existing directory or trailing-slash target gets the key basename appended.
func resolveDstPath(dst, key string) string {
	if dst == "" || strings.HasSuffix(dst, string(os.PathSeparator)) || strings.HasSuffix(dst, "/") {
		return filepath.Join(dst, path.Base(key))
	}
	if fi, err := os.Stat(dst); err == nil && fi.IsDir() {
		return filepath.Join(dst, path.Base(key))
	}
	return dst
}

// runCopy implements cp/mv. When remove is set, sources are deleted after a
// successful copy (mv).
func runCopy(cmd *cobra.Command, args []string, remove bool) error {
	recursive, _ := cmd.Flags().GetBool("recursive")
	dryrun, _ := cmd.Flags().GetBool("dryrun")
	ccFlags, _ := cmd.Flags().GetStringArray("cache-control")
	cacheRules, err := parseCacheControlFlags(ccFlags)
	if err != nil {
		return err
	}
	ctOverride, _ := cmd.Flags().GetString("content-type")
	opts := putOpts{contentType: ctOverride, cacheRules: cacheRules}
	rules := *(cmd.Context().Value(filterCtxKey).(*[]filterRule))

	srcArg, dstArg := args[0], args[1]
	srcBucket, srcKey, srcRemote := parseObjectURL(srcArg)
	dstBucket, dstKey, dstRemote := parseObjectURL(dstArg)
	if !srcRemote && !dstRemote {
		return fmt.Errorf("at least one of SRC/DST must be a remote fps:// URL")
	}
	ctx := context.Background()
	c := getClient()
	verb := "copy"
	if remove {
		verb = "move"
	}

	return retryOnAuth(func(force bool) error {
		var srcR, dstR *remote
		if srcRemote {
			cli, b, err := bucketS3Client(ctx, c, srcBucket, force)
			if err != nil {
				return err
			}
			srcR = &remote{cli, b}
		}
		if dstRemote {
			cli, b, err := bucketS3Client(ctx, c, dstBucket, force)
			if err != nil {
				return err
			}
			dstR = &remote{cli, b}
		}

		if !recursive {
			// single object/file
			var sName, sKey string
			if srcRemote {
				sKey = srcKey
				sName = srcKey
			} else {
				sName = srcArg
			}
			if !includedByFilters(rules, path.Base(sName)) {
				return nil
			}
			dKeyOrPath := dstKey
			dLocal := dstArg
			if dstRemote {
				dKeyOrPath = resolveDstKey(dstKey, sName)
			} else if srcArg != "-" {
				dLocal = resolveDstPath(dstArg, sKey)
			}
			logTransfer(verb, srcArg, dstArg, dryrun)
			if dryrun {
				return nil
			}
			if err := copyOne(ctx, srcR, mux(srcRemote, sKey, srcArg), dstR, mux(dstRemote, dKeyOrPath, dLocal), opts); err != nil {
				return err
			}
			if remove {
				return removeSource(ctx, srcR, srcRemote, sKey, srcArg)
			}
			return nil
		}

		// recursive
		if srcRemote {
			return copyRemoteTreeThenMaybeDelete(ctx, srcR, srcKey, dstR, dstKey, dstArg, dstRemote, rules, dryrun, remove, verb, opts)
		}
		return uploadLocalTree(ctx, srcArg, dstR, dstKey, rules, dryrun, remove, verb, opts)
	})
}

// mux returns key when remote, else local.
func mux(remote bool, key, local string) string {
	if remote {
		return key
	}
	return local
}

func removeSource(ctx context.Context, srcR *remote, srcRemote bool, key, local string) error {
	if srcRemote {
		_, err := srcR.cli.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &srcR.bucket, Key: &key})
		return err
	}
	if local == "-" {
		return nil
	}
	return os.Remove(local)
}

// uploadLocalTree walks a local dir and uploads every file under a dest prefix.
func uploadLocalTree(ctx context.Context, srcDir string, dstR *remote, dstPrefix string, rules []filterRule, dryrun, remove bool, verb string, opts putOpts) error {
	return filepath.Walk(srcDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !includedByFilters(rules, rel) {
			return nil
		}
		key := joinKey(dstPrefix, rel)
		logTransfer(verb, p, "fps://"+dstR.bucket+"/"+key, dryrun)
		if dryrun {
			return nil
		}
		if err := copyOne(ctx, nil, p, dstR, key, opts); err != nil {
			return err
		}
		if remove {
			return os.Remove(p)
		}
		return nil
	})
}

// copyRemoteTreeThenMaybeDelete lists a remote prefix and copies each object to
// a local dir or another remote prefix, deleting sources for mv.
func copyRemoteTreeThenMaybeDelete(ctx context.Context, srcR *remote, srcPrefix string, dstR *remote, dstPrefix, dstLocal string, dstRemote bool, rules []filterRule, dryrun, remove bool, verb string, opts putOpts) error {
	return listObjects(ctx, srcR.cli, srcR.bucket, srcPrefix, "", func(o s3types.Object) error {
		key := aws.ToString(o.Key)
		rel := strings.TrimPrefix(key, srcPrefix)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			rel = path.Base(key)
		}
		if !includedByFilters(rules, rel) {
			return nil
		}
		var dstDesc string
		if dstRemote {
			dstKey := joinKey(dstPrefix, rel)
			dstDesc = "fps://" + dstR.bucket + "/" + dstKey
			logTransfer(verb, "fps://"+srcR.bucket+"/"+key, dstDesc, dryrun)
			if dryrun {
				return nil
			}
			if err := copyOne(ctx, srcR, key, dstR, dstKey, opts); err != nil {
				return err
			}
		} else {
			outPath := filepath.Join(dstLocal, filepath.FromSlash(rel))
			logTransfer(verb, "fps://"+srcR.bucket+"/"+key, outPath, dryrun)
			if dryrun {
				return nil
			}
			if err := copyOne(ctx, srcR, key, nil, outPath, putOpts{}); err != nil {
				return err
			}
		}
		if remove {
			if _, err := srcR.cli.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &srcR.bucket, Key: &key}); err != nil {
				return err
			}
		}
		return nil
	}, nil)
}

func logTransfer(verb, src, dst string, dryrun bool) {
	if !verbose() {
		return
	}
	prefix := ""
	if dryrun {
		prefix = "(dryrun) "
	}
	fmt.Printf("%s%s: %s -> %s\n", prefix, verb, src, dst)
}

// filterCtxKey carries the parsed exclude/include rules into RunE (they are
// populated during flag parse, before RunE runs).
type ctxKey string

const filterCtxKey ctxKey = "storage-filters"

var storageCpCmd = &cobra.Command{
	Use:   "cp SRC DST",
	Short: "Copy objects between local paths and buckets ('-' = stdin/stdout)",
	Long: "Copy files to/from a bucket, or between buckets.\n\n" +
		"SRC/DST may be local paths or fps://bucket/key URLs (s3:// accepted).\n" +
		"Use '-' for stdin (SRC) or stdout (DST). --recursive copies trees.\n" +
		"Large files use multipart automatically. Garage has no storage-class/\n" +
		"SSE/ACL, so those aws flags are unsupported.",
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error { return runCopy(cmd, args, false) },
}

var storageMvCmd = &cobra.Command{
	Use:   "mv SRC DST",
	Short: "Move objects (copy then delete source)",
	Args:  cobra.ExactArgs(2),
	RunE:  func(cmd *cobra.Command, args []string) error { return runCopy(cmd, args, true) },
}

// --- rm ---------------------------------------------------------------------

var storageRmCmd = &cobra.Command{
	Use:   "rm fps://bucket/key",
	Short: "Delete objects (--recursive empties a prefix or whole bucket)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket, key, isRemote := parseObjectURL(args[0])
		if !isRemote {
			return fmt.Errorf("rm takes a remote URL: fps://<bucket>/<key>")
		}
		recursive, _ := cmd.Flags().GetBool("recursive")
		dryrun, _ := cmd.Flags().GetBool("dryrun")
		rules := *(cmd.Context().Value(filterCtxKey).(*[]filterRule))
		ctx := context.Background()
		c := getClient()

		return retryOnAuth(func(force bool) error {
			cli, s3bucket, err := bucketS3Client(ctx, c, bucket, force)
			if err != nil {
				return err
			}
			if !recursive {
				if key == "" {
					return fmt.Errorf("refusing to delete without a key; pass --recursive to empty a bucket")
				}
				if verbose() {
					fmt.Printf("%sdelete: fps://%s/%s\n", dryPrefix(dryrun), s3bucket, key)
				}
				if dryrun {
					return nil
				}
				_, err := cli.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s3bucket, Key: &key})
				return err
			}
			// recursive: list + batch delete
			var batch []s3types.ObjectIdentifier
			flush := func() error {
				if len(batch) == 0 || dryrun {
					batch = nil
					return nil
				}
				_, err := cli.DeleteObjects(ctx, &s3.DeleteObjectsInput{
					Bucket: &s3bucket,
					Delete: &s3types.Delete{Objects: batch, Quiet: aws.Bool(true)},
				})
				batch = nil
				return err
			}
			err = listObjects(ctx, cli, s3bucket, key, "", func(o s3types.Object) error {
				k := aws.ToString(o.Key)
				rel := strings.TrimPrefix(strings.TrimPrefix(k, key), "/")
				if rel == "" {
					rel = path.Base(k)
				}
				if !includedByFilters(rules, rel) {
					return nil
				}
				if verbose() {
					fmt.Printf("%sdelete: fps://%s/%s\n", dryPrefix(dryrun), s3bucket, k)
				}
				batch = append(batch, s3types.ObjectIdentifier{Key: aws.String(k)})
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
	},
}

func dryPrefix(dryrun bool) string {
	if dryrun {
		return "(dryrun) "
	}
	return ""
}

// --- sync -------------------------------------------------------------------

// fileMeta is size + mtime for a sync candidate.
type fileMeta struct {
	size  int64
	mtime time.Time
}

var storageSyncCmd = &cobra.Command{
	Use:   "sync SRC DST",
	Short: "Sync a directory/prefix to another (copies changed files only)",
	Long: "Recursively sync SRC to DST, copying only files that are missing at\n" +
		"the destination, differ in size, or whose source mtime is newer.\n" +
		"--size-only compares size alone. --delete removes dest-only files.\n" +
		"Nothing is deleted without --delete.",
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		del, _ := cmd.Flags().GetBool("delete")
		sizeOnly, _ := cmd.Flags().GetBool("size-only")
		dryrun, _ := cmd.Flags().GetBool("dryrun")
		rules := *(cmd.Context().Value(filterCtxKey).(*[]filterRule))
		ccFlags, _ := cmd.Flags().GetStringArray("cache-control")
		cacheRules, err := parseCacheControlFlags(ccFlags)
		if err != nil {
			return err
		}
		return runSync(args[0], args[1], syncOptions{
			del: del, sizeOnly: sizeOnly, dryrun: dryrun, rules: rules,
			put: putOpts{cacheRules: cacheRules},
		})
	},
}

// syncOptions carries the sync behaviour flags so `website deploy` can reuse
// the same engine as `storage sync`.
type syncOptions struct {
	del      bool
	sizeOnly bool
	dryrun   bool
	rules    []filterRule
	put      putOpts // Content-Type / Cache-Control applied to uploaded objects
}

// runSync is the sync engine behind `storage sync SRC DST` (and `website
// deploy`): copy missing/changed files, optionally delete dest-only ones.
func runSync(srcArg, dstArg string, opts syncOptions) error {
	del, sizeOnly, dryrun, rules := opts.del, opts.sizeOnly, opts.dryrun, opts.rules

	srcBucket, srcPrefix, srcRemote := parseObjectURL(srcArg)
	dstBucket, dstPrefix, dstRemote := parseObjectURL(dstArg)
	if !srcRemote && !dstRemote {
		return fmt.Errorf("at least one of SRC/DST must be a remote fps:// URL")
	}
	ctx := context.Background()
	c := getClient()

	return retryOnAuth(func(force bool) error {
		var srcR, dstR *remote
		if srcRemote {
			cli, b, err := bucketS3Client(ctx, c, srcBucket, force)
			if err != nil {
				return err
			}
			srcR = &remote{cli, b}
		}
		if dstRemote {
			cli, b, err := bucketS3Client(ctx, c, dstBucket, force)
			if err != nil {
				return err
			}
			dstR = &remote{cli, b}
		}

		srcList, err := listMeta(ctx, srcR, srcRemote, srcArg, srcPrefix, true)
		if err != nil {
			return err
		}
		dstList, err := listMeta(ctx, dstR, dstRemote, dstArg, dstPrefix, false)
		if err != nil {
			return err
		}

		rels := make([]string, 0, len(srcList))
		for rel := range srcList {
			rels = append(rels, rel)
		}
		sort.Strings(rels)
		for _, rel := range rels {
			if !includedByFilters(rules, rel) {
				continue
			}
			s := srcList[rel]
			d, ok := dstList[rel]
			need := !ok || s.size != d.size
			if !need && !sizeOnly {
				need = s.mtime.After(d.mtime)
			}
			if !need {
				continue
			}
			if err := syncCopyOne(ctx, srcR, srcRemote, srcArg, srcPrefix, dstR, dstRemote, dstArg, dstPrefix, rel, dryrun, opts.put); err != nil {
				return err
			}
		}
		if del {
			drels := make([]string, 0, len(dstList))
			for rel := range dstList {
				drels = append(drels, rel)
			}
			sort.Strings(drels)
			for _, rel := range drels {
				if _, ok := srcList[rel]; ok {
					continue
				}
				if !includedByFilters(rules, rel) {
					continue // excluded files are exempt from deletion
				}
				if err := syncDeleteOne(ctx, dstR, dstRemote, dstArg, dstPrefix, rel, dryrun); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// listMeta builds a rel->meta map for a local dir or a remote prefix. mustExist
// distinguishes the two sides: a missing local *source* is a user error (an
// empty map there would make --delete wipe the destination), while a missing
// local *destination* is just a directory the download will create.
func listMeta(ctx context.Context, r *remote, isRemote bool, localArg, prefix string, mustExist bool) (map[string]fileMeta, error) {
	out := map[string]fileMeta{}
	if isRemote {
		err := listObjects(ctx, r.cli, r.bucket, prefix, "", func(o s3types.Object) error {
			rel := strings.TrimPrefix(strings.TrimPrefix(aws.ToString(o.Key), prefix), "/")
			if rel == "" {
				return nil
			}
			out[rel] = fileMeta{size: aws.ToInt64(o.Size), mtime: aws.ToTime(o.LastModified)}
			return nil
		}, nil)
		return out, err
	}
	root := localArg
	fi, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) && !mustExist {
			return out, nil
		}
		return nil, fmt.Errorf("sync source %q: %w", root, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("sync source/target %q must be a directory", root)
	}
	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = fileMeta{size: info.Size(), mtime: info.ModTime()}
		return nil
	})
	return out, err
}

func syncCopyOne(ctx context.Context, srcR *remote, srcRemote bool, srcArg, srcPrefix string, dstR *remote, dstRemote bool, dstArg, dstPrefix, rel string, dryrun bool, put putOpts) error {
	srcDesc := filepath.Join(srcArg, filepath.FromSlash(rel))
	if srcRemote {
		srcDesc = "fps://" + srcR.bucket + "/" + joinKey(srcPrefix, rel)
	}
	dstDesc := filepath.Join(dstArg, filepath.FromSlash(rel))
	if dstRemote {
		dstDesc = "fps://" + dstR.bucket + "/" + joinKey(dstPrefix, rel)
	}
	if verbose() {
		fmt.Printf("%scopy: %s -> %s\n", dryPrefix(dryrun), srcDesc, dstDesc)
	}
	if dryrun {
		return nil
	}
	srcKeyOrPath := filepath.Join(srcArg, filepath.FromSlash(rel))
	if srcRemote {
		srcKeyOrPath = joinKey(srcPrefix, rel)
	}
	dstKeyOrPath := filepath.Join(dstArg, filepath.FromSlash(rel))
	if dstRemote {
		dstKeyOrPath = joinKey(dstPrefix, rel)
	}
	return copyOne(ctx, srcR, srcKeyOrPath, dstR, dstKeyOrPath, put)
}

func syncDeleteOne(ctx context.Context, dstR *remote, dstRemote bool, dstArg, dstPrefix, rel string, dryrun bool) error {
	if dstRemote {
		key := joinKey(dstPrefix, rel)
		if verbose() {
			fmt.Printf("%sdelete: fps://%s/%s\n", dryPrefix(dryrun), dstR.bucket, key)
		}
		if dryrun {
			return nil
		}
		_, err := dstR.cli.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &dstR.bucket, Key: &key})
		return err
	}
	p := filepath.Join(dstArg, filepath.FromSlash(rel))
	if verbose() {
		fmt.Printf("%sdelete: %s\n", dryPrefix(dryrun), p)
	}
	if dryrun {
		return nil
	}
	return os.Remove(p)
}

// --- cat --------------------------------------------------------------------

var storageCatCmd = &cobra.Command{
	Use:   "cat fps://bucket/key",
	Short: "Stream an object to stdout (optional --range)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket, key, isRemote := parseObjectURL(args[0])
		if !isRemote || key == "" {
			return fmt.Errorf("cat takes a remote URL: fps://<bucket>/<key>")
		}
		rangeSpec, _ := cmd.Flags().GetString("range")
		ctx := context.Background()
		c := getClient()
		return retryOnAuth(func(force bool) error {
			cli, s3bucket, err := bucketS3Client(ctx, c, bucket, force)
			if err != nil {
				return err
			}
			in := &s3.GetObjectInput{Bucket: &s3bucket, Key: &key}
			if rangeSpec != "" {
				r := "bytes=" + rangeSpec
				in.Range = &r
			}
			out, err := cli.GetObject(ctx, in)
			if err != nil {
				return err
			}
			defer out.Body.Close()
			_, err = io.Copy(os.Stdout, out.Body)
			return err
		})
	},
}

// --- du ---------------------------------------------------------------------

var storageDuCmd = &cobra.Command{
	Use:   "du [fps://bucket[/prefix]]",
	Short: "Sum object sizes under a prefix",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket, prefix, isRemote := parseObjectURL(args[0])
		if !isRemote {
			return fmt.Errorf("du takes a remote URL: fps://<bucket>[/prefix]")
		}
		ctx := context.Background()
		c := getClient()
		var total, count int64
		err := retryOnAuth(func(force bool) error {
			total, count = 0, 0
			cli, s3bucket, err := bucketS3Client(ctx, c, bucket, force)
			if err != nil {
				return err
			}
			return listObjects(ctx, cli, s3bucket, prefix, "", func(o s3types.Object) error {
				total += aws.ToInt64(o.Size)
				count++
				return nil
			}, nil)
		})
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(map[string]any{"total_size": total, "total_objects": count})
		}
		fmt.Printf("%s %s objects  %s\n", fmtSize(total, true), strconv.FormatInt(count, 10), args[0])
		return nil
	},
}

// --- presign ----------------------------------------------------------------

var storagePresignCmd = &cobra.Command{
	Use:   "presign fps://bucket/key",
	Short: "Generate a time-limited signed GET URL (signed locally)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket, key, isRemote := parseObjectURL(args[0])
		if !isRemote || key == "" {
			return fmt.Errorf("presign takes a remote URL: fps://<bucket>/<key>")
		}
		expires, _ := cmd.Flags().GetInt64("expires-in")
		if expires <= 0 {
			expires = 3600
		}
		if expires > 604800 {
			expires = 604800 // S3 max 7 days
		}
		ctx := context.Background()
		c := getClient()
		var url string
		err := retryOnAuth(func(force bool) error {
			cli, s3bucket, err := bucketS3Client(ctx, c, bucket, force)
			if err != nil {
				return err
			}
			ps := s3.NewPresignClient(cli)
			req, err := ps.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &s3bucket, Key: &key}, func(o *s3.PresignOptions) {
				o.Expires = time.Duration(expires) * time.Second
			})
			if err != nil {
				return err
			}
			url = req.URL
			return nil
		})
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(map[string]any{"url": url, "expires_in": expires})
		}
		fmt.Println(url)
		return nil
	},
}

// --- mb / rb (aws muscle memory) --------------------------------------------

// stripScheme returns the bucket name from a bare name or an fps://name URL.
func stripScheme(s string) string {
	if b, _, isRemote := parseObjectURL(s); isRemote {
		return b
	}
	return s
}

var storageMbCmd = &cobra.Command{
	Use:   "mb <name>",
	Short: "Make a bucket (alias for storage bucket create)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		name := stripScheme(args[0])
		c := getClient()
		b, err := c.CreateBucket(context.Background(), projectID, client.CreateBucketRequest{Name: name})
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(b)
		}
		fmt.Printf("make_bucket: %s\n", b.Name)
		if b.SecretAccessKey != "" {
			fmt.Println(mutedStyle.Render("Secret key (shown once): " + b.SecretAccessKey))
		}
		return nil
	},
}

var storageRbCmd = &cobra.Command{
	Use:   "rb <name>",
	Short: "Remove a bucket (alias for storage bucket delete; must be empty)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := stripScheme(args[0])
		c := getClient()
		id, err := resolveBucketID(c, name)
		if err != nil {
			return err
		}
		if err := c.DeleteBucket(context.Background(), id); err != nil {
			return err
		}
		fmt.Printf("remove_bucket: %s\n", name)
		return nil
	},
}

func init() {
	storageLsCmd.Flags().BoolP("recursive", "r", false, "List all keys, not just the next '/' level")
	storageLsCmd.Flags().Bool("human-readable", false, "Print sizes with binary units")
	storageLsCmd.Flags().Bool("summarize", false, "Print total object count and size")

	storageCpCmd.Flags().BoolP("recursive", "r", false, "Copy directories/prefixes recursively")
	storageCpCmd.Flags().Bool("dryrun", false, "Print what would be copied without transferring")
	storageCpCmd.Flags().String("content-type", "", "Force the uploaded Content-Type (default: inferred from extension)")
	storageCpCmd.Flags().StringArray("cache-control", nil, cacheControlFlagHelp)
	cpFilters := addFilterFlags(storageCpCmd)

	storageMvCmd.Flags().BoolP("recursive", "r", false, "Move directories/prefixes recursively")
	storageMvCmd.Flags().Bool("dryrun", false, "Print what would be moved without transferring")
	storageMvCmd.Flags().String("content-type", "", "Force the uploaded Content-Type (default: inferred from extension)")
	storageMvCmd.Flags().StringArray("cache-control", nil, cacheControlFlagHelp)
	mvFilters := addFilterFlags(storageMvCmd)

	storageRmCmd.Flags().BoolP("recursive", "r", false, "Delete every key under the prefix (empties a bucket)")
	storageRmCmd.Flags().Bool("dryrun", false, "Print what would be deleted without deleting")
	rmFilters := addFilterFlags(storageRmCmd)

	storageSyncCmd.Flags().Bool("delete", false, "Delete dest-only files (excluded files are exempt)")
	storageSyncCmd.Flags().Bool("size-only", false, "Compare size only (ignore mtime)")
	storageSyncCmd.Flags().Bool("dryrun", false, "Print what would change without transferring")
	storageSyncCmd.Flags().StringArray("cache-control", nil, cacheControlFlagHelp)
	syncFilters := addFilterFlags(storageSyncCmd)

	storageCatCmd.Flags().String("range", "", "Byte range x-y (sends a Range header)")

	storageDuCmd.Flags().BoolP("summarize", "s", false, "Print only the grand total")

	storagePresignCmd.Flags().Int64("expires-in", 3600, "URL lifetime in seconds (max 604800)")

	// Inject the parsed filter rules into each command's context so RunE can
	// read them (Set is called during flag parse, before RunE runs).
	for cmd, rules := range map[*cobra.Command]*[]filterRule{
		storageCpCmd:   cpFilters,
		storageMvCmd:   mvFilters,
		storageRmCmd:   rmFilters,
		storageSyncCmd: syncFilters,
	} {
		r := rules
		cmd.PreRun = func(c *cobra.Command, _ []string) {
			c.SetContext(context.WithValue(c.Context(), filterCtxKey, r))
		}
	}

	storageCmd.AddCommand(
		storageLsCmd,
		storageCpCmd,
		storageMvCmd,
		storageRmCmd,
		storageSyncCmd,
		storageCatCmd,
		storageDuCmd,
		storagePresignCmd,
		storageMbCmd,
		storageRbCmd,
	)
}
