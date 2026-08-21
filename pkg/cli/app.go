package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

// Time helpers (allow testing overrides).
var (
	timeNow   = time.Now
	timeParse = time.Parse
)

// resolveAppID turns a user-supplied app reference (name or id) into an app id.
// A UUID is returned as-is (and needs no project); a plain name is looked up in
// the current project, where app names are unique.
func resolveAppID(c *client.Client, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("app name or id is required")
	}
	if looksLikeUUID(ref) {
		return ref, nil
	}
	project, err := requireProject()
	if err != nil {
		return "", fmt.Errorf("resolve app %q: %w", ref, err)
	}
	apps, err := c.ListApps(context.Background(), project)
	if err != nil {
		return "", err
	}
	for _, a := range apps {
		if a.Name == ref {
			return a.ID, nil
		}
	}
	return "", notFoundf("app %q not found in project %q", ref, project)
}

// appRefFrom returns the app reference (name or id) for a command that accepts
// it either via the --app flag or a positional shorthand. The flag wins when
// both are given.
func appRefFrom(cmd *cobra.Command, args []string) string {
	if ref, _ := cmd.Flags().GetString("app"); ref != "" {
		return ref
	}
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// appIDFrom resolves the target app id for such a command, accepting a name or
// id from either --app or the positional argument.
func appIDFrom(c *client.Client, cmd *cobra.Command, args []string) (string, error) {
	return resolveAppID(c, appRefFrom(cmd, args))
}

// looksLikeUUID reports whether s has the 8-4-4-4-12 hex shape of a UUID.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if ch != '-' {
				return false
			}
			continue
		}
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}

var appCmd = &cobra.Command{
	Use:     "app",
	Aliases: []string{"apps"},
	Short:   "Manage apps",
}

// slugDisplay renders an app's optional vanity URL slug, showing an em-dash when
// unset (the app uses its derived host label).
func slugDisplay(slug string) string {
	if slug == "" {
		return mutedStyle.Render("—")
	}
	return slug
}

// ingressDisplay renders an app's ingress setting, or an em-dash for a worker —
// which has no Service to be reachable on, so neither "all" nor "internal"
// describes it.
func ingressDisplay(app *client.App) string {
	if app.Type == "worker" {
		return mutedStyle.Render("—")
	}
	return app.Ingress
}

// missingAppFields names what `app create` was going to ask for, so the refusal
// is specific to the call that hit it.
func missingAppFields(name, image string) string {
	switch {
	case name == "" && image == "":
		return "name the app and its image: fpcloud app create <name> --image <image>"
	case name == "":
		return "name the app: fpcloud app create <name>"
	default:
		return "pass --image"
	}
}

var appCreateCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a new app",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}

		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		image, _ := cmd.Flags().GetString("image")
		port, _ := cmd.Flags().GetInt("port")
		replicas, _ := cmd.Flags().GetInt("replicas")
		ingress, _ := cmd.Flags().GetString("ingress")
		mode, _ := cmd.Flags().GetString("mode")
		appType, _ := cmd.Flags().GetString("type")
		storage, _ := cmd.Flags().GetString("storage")
		storagePath, _ := cmd.Flags().GetString("storage-path")

		// Prompt for missing required fields.
		if name == "" || image == "" {
			if err := requirePrompt(missingAppFields(name, image)); err != nil {
				return err
			}
			fields := []huh.Field{}
			if name == "" {
				fields = append(fields, huh.NewInput().Title("App name").Value(&name).Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("name is required")
					}
					return nil
				}))
			}
			if image == "" {
				fields = append(fields, huh.NewInput().Title("Container image").Description("e.g. nginx:latest, ghcr.io/user/app:v1").Value(&image).Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("image is required")
					}
					return nil
				}))
			}
			form := huh.NewForm(huh.NewGroup(fields...))
			if err := form.Run(); err != nil {
				return err
			}
		}

		serviceAccount, _ := cmd.Flags().GetString("service-account")
		healthCheckPath, _ := cmd.Flags().GetString("health-check-path")
		healthCheckTimeout, _ := cmd.Flags().GetInt("health-check-timeout")
		healthCheckInterval, _ := cmd.Flags().GetInt("health-check-interval")
		healthCheckRetries, _ := cmd.Flags().GetInt("health-check-retries")
		command, _ := cmd.Flags().GetStringArray("command")
		cmdArgs, _ := cmd.Flags().GetStringArray("arg")
		releaseCommand, _ := cmd.Flags().GetStringArray("release-command")
		mountFlags, _ := cmd.Flags().GetStringArray("mount")
		mounts, err := parseVolumeMounts(mountFlags)
		if err != nil {
			return err
		}
		routeFlags, _ := cmd.Flags().GetStringArray("route")
		routes, err := parseRoutes(routeFlags)
		if err != nil {
			return err
		}
		probes, err := probeOverridesFromFlags(cmd)
		if err != nil {
			return err
		}
		secCtx := securityContextFromFlags(cmd)
		displayName, _ := cmd.Flags().GetString("display-name")
		slug, _ := cmd.Flags().GetString("slug")

		// --port and --health-check-path carry defaults, so their value alone
		// cannot say whether the user asked for them. A worker has neither and
		// the API refuses both, so drop the untyped defaults — that way the
		// refusal answers something the user actually wrote.
		if appType == "worker" {
			if !cmd.Flags().Changed("port") {
				port = 0
			}
			if !cmd.Flags().Changed("health-check-path") {
				healthCheckPath = ""
			}
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		c := getClient()

		var app *client.App
		var createErr error
		action := func() {
			app, createErr = c.CreateApp(context.Background(), projectID, client.CreateAppRequest{
				Name:                name,
				DisplayName:         displayName,
				URLSlug:             slug,
				Image:               image,
				Command:             command,
				Args:                cmdArgs,
				ReleaseCommand:      releaseCommand,
				VolumeMounts:        mounts,
				SecurityContext:     secCtx,
				Port:                port,
				Replicas:            replicas,
				Ingress:             ingress,
				Routes:              routes,
				Mode:                mode,
				Type:                appType,
				Storage:             storage,
				StoragePath:         storagePath,
				ServiceAccount:      serviceAccount,
				HealthCheckPath:     healthCheckPath,
				HealthCheckTimeout:  healthCheckTimeout,
				HealthCheckInterval: healthCheckInterval,
				HealthCheckRetries:  healthCheckRetries,
				Probes:              probes,
			})
		}

		if !isStructured(outputFormat) {
			withSpinner("Creating app...", action)
		} else {
			action()
		}
		if createErr != nil {
			return createErr
		}

		if isStructured(outputFormat) {
			return renderData(app)
		}

		rows := [][]string{
			{"ID", mutedStyle.Render(app.ID)},
			{"Name", app.Name},
			{"Display Name", app.DisplayName},
			{"Image", app.Image},
			{"Type", app.Type},
			{"Mode", app.Mode},
			{"Status", renderStatus(app.Status)},
		}
		// A worker has no slug, ingress or URL. Printing those rows empty would
		// read as "not configured yet" rather than "does not apply".
		if app.Type != "worker" {
			rows = append(rows,
				[]string{"URL Slug", slugDisplay(app.URLSlug)},
				[]string{"Ingress", app.Ingress},
				[]string{"URL", lipgloss.NewStyle().Bold(true).Foreground(colorInfo).Render(app.URL)},
			)
		}
		fmt.Println(renderInfoBox("App Created", rows))
		return nil
	},
}

// parseVolumeMounts turns repeated --mount flags of the form
// "source:name:mount-path[:sub-path]" into VolumeMount records. source must be
// "configmap", "secret", or "emptydir" (emptydir takes an empty name, e.g.
// "emptydir::/tmp").
func parseVolumeMounts(flags []string) ([]client.VolumeMount, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	mounts := make([]client.VolumeMount, 0, len(flags))
	for _, f := range flags {
		parts := strings.SplitN(f, ":", 4)
		if len(parts) < 3 {
			return nil, fmt.Errorf("invalid --mount %q (want source:name:mount-path[:sub-path])", f)
		}
		if parts[0] != "configmap" && parts[0] != "secret" && parts[0] != "emptydir" {
			return nil, fmt.Errorf("invalid --mount source %q (must be configmap, secret, or emptydir)", parts[0])
		}
		if parts[1] == "" && parts[0] != "emptydir" {
			return nil, fmt.Errorf("invalid --mount %q (name is required for %s)", f, parts[0])
		}
		m := client.VolumeMount{Source: parts[0], Name: parts[1], MountPath: parts[2]}
		if len(parts) == 4 {
			m.SubPath = parts[3]
		}
		mounts = append(mounts, m)
	}
	return mounts, nil
}

// parseRoutes turns repeated --route flags of the form "path[:visibility]" into
// Route records (#501). Visibility defaults to "internal" — carving a path out
// is the only reason to name one, since every path of a public app is already
// public.
func parseRoutes(flags []string) ([]client.Route, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	routes := make([]client.Route, 0, len(flags))
	for _, f := range flags {
		path, visibility, found := strings.Cut(f, ":")
		if !found {
			visibility = "internal"
		}
		if path == "" {
			return nil, fmt.Errorf("invalid --route %q (want path[:visibility], e.g. /internal/)", f)
		}
		if visibility != "internal" && visibility != "public" {
			return nil, fmt.Errorf("invalid --route visibility %q (must be internal or public)", visibility)
		}
		routes = append(routes, client.Route{Path: path, Visibility: visibility})
	}
	return routes, nil
}

// probeFieldNames documents the keys accepted inside --liveness/--readiness/--startup.
// They are the wire field names verbatim so the CLI, API and Terraform spell one
// probe exactly one way (ADR-048).
var probeFieldNames = []string{
	"path",
	"initial_delay_seconds",
	"period_seconds",
	"timeout_seconds",
	"failure_threshold",
	"success_threshold",
}

// parseProbeSpec parses one `key=value,key=value` probe override (#453), e.g.
// "path=/healthz,period_seconds=5". Every field is independently optional: an
// omitted one falls back to the app's shared --health-check-* value rather than
// to a hardcoded default, so overriding a liveness path never silently restates
// the timing.
func parseProbeSpec(flag, value string) (*client.ProbeSpec, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	spec := &client.ProbeSpec{}
	for _, field := range strings.Split(value, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		key, raw, found := strings.Cut(field, "=")
		key, raw = strings.TrimSpace(key), strings.TrimSpace(raw)
		if !found || key == "" || raw == "" {
			return nil, fmt.Errorf("invalid --%s %q (want key=value, one of %s)", flag, field, strings.Join(probeFieldNames, ", "))
		}
		if key == "path" {
			if !strings.HasPrefix(raw, "/") {
				return nil, fmt.Errorf("invalid --%s path %q (must start with \"/\")", flag, raw)
			}
			spec.Path = raw
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid --%s %s=%q (must be a positive whole number of seconds or attempts)", flag, key, raw)
		}
		switch key {
		case "initial_delay_seconds":
			spec.InitialDelaySeconds = n
		case "period_seconds":
			spec.PeriodSeconds = n
		case "timeout_seconds":
			spec.TimeoutSeconds = n
		case "failure_threshold":
			spec.FailureThreshold = n
		case "success_threshold":
			spec.SuccessThreshold = n
		default:
			return nil, fmt.Errorf("unknown --%s field %q (want one of %s)", flag, key, strings.Join(probeFieldNames, ", "))
		}
	}
	return spec, nil
}

// probeOverridesFromFlags assembles the per-probe overrides from --liveness,
// --readiness and --startup, returning nil when none is set (every probe then
// uses the shared --health-check-* shorthand).
func probeOverridesFromFlags(cmd *cobra.Command) (*client.ProbeOverrides, error) {
	liveness, _ := cmd.Flags().GetString("liveness")
	readiness, _ := cmd.Flags().GetString("readiness")
	startup, _ := cmd.Flags().GetString("startup")

	overrides := &client.ProbeOverrides{}
	for _, p := range []struct {
		flag  string
		value string
		dest  **client.ProbeSpec
	}{
		{"liveness", liveness, &overrides.Liveness},
		{"readiness", readiness, &overrides.Readiness},
		{"startup", startup, &overrides.Startup},
	} {
		spec, err := parseProbeSpec(p.flag, p.value)
		if err != nil {
			return nil, err
		}
		*p.dest = spec
	}
	if overrides.Liveness == nil && overrides.Readiness == nil && overrides.Startup == nil {
		return nil, nil
	}
	return overrides, nil
}

// registerProbeFlags declares --liveness/--readiness/--startup on a command, so
// `app create` and `app set-probes` spell an override identically.
func registerProbeFlags(cmd *cobra.Command) {
	suffix := " probe override as key=value,... (fields: " + strings.Join(probeFieldNames, ", ") + "); anything left out keeps the --health-check-* value"
	cmd.Flags().String("liveness", "", "Liveness"+suffix+", e.g. path=/healthz")
	cmd.Flags().String("readiness", "", "Readiness"+suffix+", e.g. path=/ready,failure_threshold=2")
	cmd.Flags().String("startup", "", "Startup"+suffix+", e.g. path=/healthz,failure_threshold=30")
}

// securityContextFromFlags assembles a SecurityContext from the hardening flags,
// returning nil when none are set (leaving the image default). The numeric id
// flags default to -1 = "unset".
func securityContextFromFlags(cmd *cobra.Command) *client.SecurityContext {
	runAsUser, _ := cmd.Flags().GetInt64("run-as-user")
	runAsGroup, _ := cmd.Flags().GetInt64("run-as-group")
	fsGroup, _ := cmd.Flags().GetInt64("fs-group")
	runAsNonRoot, _ := cmd.Flags().GetBool("run-as-non-root")
	readOnlyRootFS, _ := cmd.Flags().GetBool("read-only-root-fs")

	if runAsUser < 0 && runAsGroup < 0 && fsGroup < 0 && !runAsNonRoot && !readOnlyRootFS {
		return nil
	}
	sc := &client.SecurityContext{RunAsNonRoot: runAsNonRoot, ReadOnlyRootFilesystem: readOnlyRootFS}
	if runAsUser >= 0 {
		sc.RunAsUser = &runAsUser
	}
	if runAsGroup >= 0 {
		sc.RunAsGroup = &runAsGroup
	}
	if fsGroup >= 0 {
		sc.FSGroup = &fsGroup
	}
	return sc
}

var appListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all apps in the current project",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}

		c := getClient()
		apps, err := c.ListApps(context.Background(), projectID)
		if err != nil {
			return err
		}

		showID, _ := cmd.Flags().GetBool("show-id")
		rows := make([][]string, len(apps))
		for i, a := range apps {
			row := []string{a.Name, a.Image, a.Type, a.Mode, ingressDisplay(a), renderStatus(a.Status), a.URL}
			if showID {
				row = append([]string{a.ID}, row...)
			}
			rows[i] = row
		}
		headers := []string{"NAME", "IMAGE", "TYPE", "MODE", "INGRESS", "STATUS", "URL"}
		if showID {
			headers = append([]string{"ID"}, headers...)
		}
		render(headers, rows, apps)
		return nil
	},
}

var appGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show app details",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		appID, err := appIDFrom(c, cmd, args)
		if err != nil {
			return err
		}
		app, err := c.GetApp(context.Background(), appID)
		if err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(app)
		}

		domains := strings.Join(app.Domains, ", ")
		if domains == "" {
			domains = mutedStyle.Render("none")
		}
		saDisplay := mutedStyle.Render("none")
		if app.ServiceAccountID != "" {
			saDisplay = app.ServiceAccountID
		}

		healthCheck := app.HealthCheckPath
		if healthCheck == "" || healthCheck == "/" {
			healthCheck = mutedStyle.Render("default (/)")
		} else {
			healthCheck = fmt.Sprintf("%s (timeout=%ds, interval=%ds, retries=%d)", app.HealthCheckPath, app.HealthCheckTimeout, app.HealthCheckInterval, app.HealthCheckRetries)
		}

		// Only the fields a probe actually overrides are listed; everything else
		// it inherits is already on the Health Check row above.
		probesDisplay := mutedStyle.Render("none · all three use the health check")
		if lines := probeSummaries(app); len(lines) > 0 {
			probesDisplay = strings.Join(lines, "\n")
		}

		storageDisplay := mutedStyle.Render("none")
		if app.Storage != "" {
			path := app.StoragePath
			if path == "" {
				path = "/data"
			}
			storageDisplay = fmt.Sprintf("%s at %s", app.Storage, path)
		}

		releaseDisplay := mutedStyle.Render("none")
		if len(app.ReleaseCommand) > 0 {
			releaseDisplay = strings.Join(app.ReleaseCommand, " ")
		}

		routesDisplay := mutedStyle.Render("none")
		if len(app.Routes) > 0 {
			parts := make([]string, 0, len(app.Routes))
			for _, r := range app.Routes {
				parts = append(parts, fmt.Sprintf("%s (%s)", r.Path, r.Visibility))
			}
			routesDisplay = strings.Join(parts, ", ")
		}

		rows := [][]string{
			{"ID", mutedStyle.Render(app.ID)},
			{"Name", app.Name},
			{"Display Name", app.DisplayName},
			{"Image", app.Image},
			{"Type", app.Type},
			{"Mode", app.Mode},
			{"Status", renderStatus(app.Status)},
			{"Replicas", fmt.Sprintf("%d", app.Replicas)},
			{"Storage", storageDisplay},
			{"Release Command", releaseDisplay},
			{"Service Account", saDisplay},
		}
		// Everything below describes how the app is reached or checked. A worker
		// is reached by nothing and probed by nothing, so the rows are dropped
		// rather than shown blank.
		if app.Type != "worker" {
			rows = append(rows,
				[]string{"URL Slug", slugDisplay(app.URLSlug)},
				[]string{"Port", fmt.Sprintf("%d", app.Port)},
				[]string{"Ingress", app.Ingress},
				[]string{"Routes", routesDisplay},
				[]string{"URL", lipgloss.NewStyle().Bold(true).Foreground(colorInfo).Render(app.URL)},
				[]string{"Health Check", healthCheck},
				[]string{"Probes", probesDisplay},
				[]string{"Domains", domains},
			)
		}
		fmt.Println(renderInfoBox("App Details", rows))
		return nil
	},
}

var appDeployCmd = &cobra.Command{
	Use:   "deploy [name]",
	Short: "Deploy a new revision",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		image, _ := cmd.Flags().GetString("image")
		if image == "" {
			return fmt.Errorf("--image is required")
		}
		noTraffic, _ := cmd.Flags().GetBool("no-traffic")
		release, _ := cmd.Flags().GetString("release")

		outputFormat := rootCmd.Flag("output").Value.String()
		c := getClient()
		appID, err := appIDFrom(c, cmd, args)
		if err != nil {
			return err
		}

		var app *client.App
		var deployErr error
		action := func() {
			app, deployErr = c.DeployApp(context.Background(), appID, client.DeployRequest{
				Image:     image,
				Release:   release,
				NoTraffic: noTraffic,
			})
		}

		if !isStructured(outputFormat) {
			withSpinner("Deploying...", action)
		} else {
			action()
		}
		if deployErr != nil {
			return deployErr
		}

		// A release-gated deploy returns immediately while the release command
		// runs as a Job; follow the deployment record to its terminal status.
		if len(app.ReleaseCommand) > 0 {
			var dep *client.Deployment
			wait := func() { dep, deployErr = waitForRelease(c, appID) }
			if !isStructured(outputFormat) {
				withSpinner("Running release command...", wait)
			} else {
				wait()
			}
			if deployErr != nil {
				return deployErr
			}
			if dep.Status == "failed" {
				msg := dep.Message
				if logs := strings.TrimSpace(dep.ReleaseLogs); logs != "" {
					msg += "\n\n--- release command output ---\n" + logs
				}
				return fmt.Errorf("%s", msg)
			}
			if fresh, err := c.GetApp(context.Background(), appID); err == nil {
				app = fresh
			}
		}

		if isStructured(outputFormat) {
			return renderData(app)
		}

		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Deployed %s to %s", image, app.URL),
		))
		fmt.Println()
		fmt.Println(renderInfoBox("Deployment", [][]string{
			{"ID", mutedStyle.Render(app.ID)},
			{"Name", app.Name},
			{"Release", releaseDisplay(app.Release)},
			{"Image", app.Image},
			{"Status", renderStatus(app.Status)},
			{"URL", lipgloss.NewStyle().Bold(true).Foreground(colorInfo).Render(app.URL)},
		}))
		return nil
	},
}

var appSetRoutesCmd = &cobra.Command{
	Use:   "set-routes [name]",
	Short: "Set which of an app's paths stay off the public ingress",
	Long: "Carves path prefixes out of a public app's ingress (#501): a route marked internal is " +
		"withheld from the external ingress — on the app's own URL and on every custom domain — " +
		"while staying reachable at the app's in-cluster address, which is where a scheduled " +
		"job's self-call reaches it.\n\n" +
		"External requests to an internal path are refused at the edge and never reach the app.\n\n" +
		"Replace-in-full: the flags given are the complete set, and `--clear` removes them all. " +
		"Always-on apps with --ingress all only.",
	Example: "  fpcloud app set-routes api --route /internal/\n" +
		"  fpcloud app set-routes api --route /internal/ --route /admin/\n" +
		"  fpcloud app set-routes api --clear",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clear, _ := cmd.Flags().GetBool("clear")
		routeFlags, _ := cmd.Flags().GetStringArray("route")
		if clear && len(routeFlags) > 0 {
			return fmt.Errorf("--clear cannot be combined with --route")
		}
		if !clear && len(routeFlags) == 0 {
			return fmt.Errorf("at least one --route is required (or --clear to remove them all)")
		}
		routes, err := parseRoutes(routeFlags)
		if err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		c := getClient()
		appID, err := appIDFrom(c, cmd, args)
		if err != nil {
			return err
		}

		var app *client.App
		var updateErr error
		action := func() { app, updateErr = c.UpdateAppRoutes(context.Background(), appID, routes) }
		if !isStructured(outputFormat) {
			withSpinner("Applying routes...", action)
		} else {
			action()
		}
		if updateErr != nil {
			return updateErr
		}
		if isStructured(outputFormat) {
			return renderData(app)
		}
		msg := fmt.Sprintf(" Cleared route visibility on %s", app.Name)
		if len(app.Routes) > 0 {
			paths := make([]string, 0, len(app.Routes))
			for _, r := range app.Routes {
				paths = append(paths, fmt.Sprintf("%s (%s)", r.Path, r.Visibility))
			}
			msg = fmt.Sprintf(" Routes on %s: %s", app.Name, strings.Join(paths, ", "))
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") + msg,
		))
		return nil
	},
}

var appSetProbesCmd = &cobra.Command{
	Use:   "set-probes [name]",
	Short: "Give liveness, readiness and startup their own path and timing",
	Long: "By default all three probes share the app's --health-check-* settings, so the same " +
		"request decides both whether traffic reaches the app and whether the pod is killed. " +
		"That couples them: a health path that checks a downstream means a database blip " +
		"restarts pods that were fine.\n\n" +
		"Point liveness at a cheap, dependency-free path and readiness at the one that checks " +
		"downstreams, and a blip only pulls the pod out of the load balancer until it clears.\n\n" +
		"Each probe takes a comma-separated key=value list; any field left out keeps the " +
		"app's shared --health-check-* value, so setting a path never means restating timing. " +
		"Fields: " + strings.Join(probeFieldNames, ", ") + ". success_threshold applies to " +
		"readiness only — Kubernetes requires 1 for the other two.\n\n" +
		"Replace-in-full: the flags given are the complete set, and --clear puts all three " +
		"back on the shared shorthand. Always-on apps only.",
	Example: "  fpcloud app set-probes api --liveness path=/healthz\n" +
		"  fpcloud app set-probes api --liveness path=/healthz --readiness path=/ready,failure_threshold=2\n" +
		"  fpcloud app set-probes api --startup path=/healthz,failure_threshold=30,period_seconds=10\n" +
		"  fpcloud app set-probes api --clear",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clear, _ := cmd.Flags().GetBool("clear")
		probes, err := probeOverridesFromFlags(cmd)
		if err != nil {
			return err
		}
		if clear && probes != nil {
			return fmt.Errorf("--clear cannot be combined with --liveness, --readiness or --startup")
		}
		if !clear && probes == nil {
			return fmt.Errorf("at least one of --liveness, --readiness or --startup is required (or --clear to put all three back on the shared health check)")
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		c := getClient()
		appID, err := appIDFrom(c, cmd, args)
		if err != nil {
			return err
		}

		var app *client.App
		var updateErr error
		action := func() { app, updateErr = c.UpdateAppProbes(context.Background(), appID, probes) }
		if !isStructured(outputFormat) {
			withSpinner("Applying probes...", action)
		} else {
			action()
		}
		if updateErr != nil {
			return updateErr
		}
		if isStructured(outputFormat) {
			return renderData(app)
		}
		msg := fmt.Sprintf(" Probes on %s back on the shared health check", app.Name)
		if lines := probeSummaries(app); len(lines) > 0 {
			msg = fmt.Sprintf(" Probes on %s: %s", app.Name, strings.Join(lines, ", "))
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") + msg,
		))
		return nil
	},
}

// probeSummaries renders one "name: path (timing)" line per overridden probe,
// shared by `app set-probes` and `app get`. Only fields the app actually
// overrode are shown — the rest come from the health-check row above it, and
// repeating them would misreport a shared value as a per-probe one.
func probeSummaries(app *client.App) []string {
	if app.Probes == nil {
		return nil
	}
	var out []string
	for _, p := range []struct {
		name string
		spec *client.ProbeSpec
	}{
		{"liveness", app.Probes.Liveness},
		{"readiness", app.Probes.Readiness},
		{"startup", app.Probes.Startup},
	} {
		if p.spec == nil {
			continue
		}
		var timing []string
		for _, f := range []struct {
			key   string
			value int
		}{
			{"initial_delay", p.spec.InitialDelaySeconds},
			{"period", p.spec.PeriodSeconds},
			{"timeout", p.spec.TimeoutSeconds},
			{"failures", p.spec.FailureThreshold},
			{"successes", p.spec.SuccessThreshold},
		} {
			if f.value > 0 {
				timing = append(timing, fmt.Sprintf("%s=%d", f.key, f.value))
			}
		}
		path := p.spec.Path
		if path == "" {
			path = app.HealthCheckPath
		}
		line := fmt.Sprintf("%s %s", p.name, path)
		if len(timing) > 0 {
			line = fmt.Sprintf("%s (%s)", line, strings.Join(timing, ", "))
		}
		out = append(out, line)
	}
	return out
}

var appReconcileCmd = &cobra.Command{
	Use:   "reconcile [name]",
	Short: "Re-apply an app's runtime from control-plane state",
	Long: "Republishes the app's config and re-applies its Deployment or Knative Service, " +
		"rolling the pods only if the rendered spec differs from what is running.\n\n" +
		"Repairs an app whose cluster state has drifted. Unlike `app deploy` it changes no " +
		"image, records no deployment, and does not run the release command — so `--all` is " +
		"safe to run across a project without re-running every app's migrations.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		all, _ := cmd.Flags().GetBool("all")
		outputFormat := rootCmd.Flag("output").Value.String()
		c := getClient()

		if !all {
			appID, err := appIDFrom(c, cmd, args)
			if err != nil {
				return err
			}
			var app *client.App
			var reconcileErr error
			action := func() { app, reconcileErr = c.ReconcileApp(context.Background(), appID) }
			if !isStructured(outputFormat) {
				withSpinner("Reconciling...", action)
			} else {
				action()
			}
			if reconcileErr != nil {
				return reconcileErr
			}
			if isStructured(outputFormat) {
				return renderData(app)
			}
			fmt.Println(successBox.Render(
				lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
					fmt.Sprintf(" Reconciled %s", app.Name),
			))
			return nil
		}

		if appRefFrom(cmd, args) != "" {
			return fmt.Errorf("--all reconciles every app in the project; drop the app name")
		}
		project, err := requireProject()
		if err != nil {
			return err
		}
		apps, err := c.ListApps(context.Background(), project)
		if err != nil {
			return err
		}

		// Reconcile every app, and keep going past a failure so one broken app
		// can't strand the rest — the whole point of --all is a sweep.
		reconciled := make([]*client.App, 0, len(apps))
		var failures []string
		action := func() {
			for _, a := range apps {
				app, rerr := c.ReconcileApp(context.Background(), a.ID)
				if rerr != nil {
					failures = append(failures, fmt.Sprintf("%s: %v", a.Name, rerr))
					continue
				}
				reconciled = append(reconciled, app)
			}
		}
		if !isStructured(outputFormat) {
			withSpinner(fmt.Sprintf("Reconciling %d apps...", len(apps)), action)
		} else {
			action()
		}

		if isStructured(outputFormat) {
			if err := renderData(reconciled); err != nil {
				return err
			}
		} else {
			fmt.Println(successBox.Render(
				lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
					fmt.Sprintf(" Reconciled %d of %d apps in %s", len(reconciled), len(apps), project),
			))
		}
		if len(failures) > 0 {
			return fmt.Errorf("failed to reconcile %d app(s):\n  %s", len(failures), strings.Join(failures, "\n  "))
		}
		return nil
	},
}

// waitForRelease follows the newest deployment record until a release-gated
// deploy resolves — the API's deploy call returns while the release command is
// still running as a Job, leaving the record in 'releasing' until the gate and
// rollout finish.
func waitForRelease(c *client.Client, appID string) (*client.Deployment, error) {
	deadline := timeNow().Add(30 * time.Minute)
	for {
		deps, err := c.ListDeployments(context.Background(), appID)
		if err != nil {
			return nil, err
		}
		if len(deps) > 0 {
			switch deps[0].Status {
			case "deploying", "releasing":
			default:
				return deps[0], nil
			}
		}
		if timeNow().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for the release command to finish; check `fpcloud app deployments`")
		}
		time.Sleep(2 * time.Second)
	}
}

var appRevisionsCmd = &cobra.Command{
	Use:   "revisions <name>",
	Short: "List revisions for an app",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		appID, err := appIDFrom(c, cmd, args)
		if err != nil {
			return err
		}
		revisions, err := c.ListRevisions(context.Background(), appID)
		if err != nil {
			return err
		}

		rows := make([][]string, len(revisions))
		for i, r := range revisions {
			ready := renderStatus("stopped")
			if r.Ready {
				ready = renderStatus("running")
			}
			rows[i] = []string{r.Name, ready, r.Image, r.CreatedAt}
		}
		render([]string{"NAME", "READY", "IMAGE", "CREATED"}, rows, revisions)
		return nil
	},
}

var appScaleCmd = &cobra.Command{
	Use:   "scale <name>",
	Short: "Update scaling configuration for an app",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFormat := rootCmd.Flag("output").Value.String()
		c := getClient()
		appID, err := appIDFrom(c, cmd, args)
		if err != nil {
			return err
		}

		req := client.ScaleRequest{}
		if cmd.Flags().Changed("min") {
			v, _ := cmd.Flags().GetInt32("min")
			req.MinScale = &v
		}
		if cmd.Flags().Changed("max") {
			v, _ := cmd.Flags().GetInt32("max")
			req.MaxScale = &v
		}
		if cmd.Flags().Changed("replicas") {
			v, _ := cmd.Flags().GetInt32("replicas")
			req.Replicas = &v
		}
		if cmd.Flags().Changed("cpu") {
			req.CPULimit, _ = cmd.Flags().GetString("cpu")
		}
		if cmd.Flags().Changed("memory") {
			req.MemoryLimit, _ = cmd.Flags().GetString("memory")
		}

		var app *client.App
		var scaleErr error
		action := func() {
			app, scaleErr = c.ScaleApp(context.Background(), appID, req)
		}

		if !isStructured(outputFormat) {
			withSpinner("Scaling app...", action)
		} else {
			action()
		}
		if scaleErr != nil {
			return scaleErr
		}

		if isStructured(outputFormat) {
			return renderData(app)
		}

		rows := [][]string{
			{"ID", mutedStyle.Render(app.ID)},
			{"Name", app.Name},
		}
		if app.Mode == "always-on" {
			rows = append(rows, []string{"Replicas", fmt.Sprintf("%d", app.Replicas)})
		} else {
			rows = append(rows,
				[]string{"Min Scale", fmt.Sprintf("%d", app.MinScale)},
				[]string{"Max Scale", fmt.Sprintf("%d", app.MaxScale)},
			)
		}
		rows = append(rows,
			[]string{"CPU Limit", app.CPULimit},
			[]string{"Memory Limit", app.MemoryLimit},
			[]string{"Status", renderStatus(app.Status)},
		)
		fmt.Println(renderInfoBox("App Scaled", rows))
		return nil
	},
}

var appUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update an app's display name, vanity slug, hosting mode, storage, or container command",
	Long: `Update an app in place.

  --display-name  Change the app's cosmetic display name. The frozen name (which
                  names the k8s objects and the URL) is untouched — no redeploy.
  --slug          Set the vanity URL slug so the app is reachable at
                  <slug>.app.<platform-domain>. Pass --slug "" to clear it back to
                  the derived host. Reconciles the Ingress; always-on apps only.
  --mode          Switch between hosting modes. 'always-on' is an always-on
                  Deployment; 'serverless' is a scale-to-zero Knative Service. The
                  switch recreates the runtime, preserving image, env, and secrets.
  --storage       Grow the app's persistent volume (e.g. 100Gi). Grow-only — the
                  volume can never shrink — and only for always-on apps with storage.
  --command       Override the container entrypoint (repeatable). Passing the flag
                  with no value clears the override back to the image ENTRYPOINT.
                  Triggers a redeploy so the running pod picks up the new entrypoint.
  --arg           Container arguments (repeatable). Passing the flag with no value
                  clears them back to the image CMD.
  --release-command  Command run once per deploy — from the exact image being
                  deployed, with the app's env/secrets — before the new version
                  goes live; a failure aborts the deploy (e.g. DB migrations).
                  A single string with spaces runs via 'sh -c'; repeat the flag
                  for exec form. Pass with no value to drop the release phase.
                  Applies on the next deploy — no redeploy now.
  --run-as-user, --run-as-group, --fs-group, --run-as-non-root, --read-only-root-fs
                  Replace the app's security context. Passing any of them sets the
                  whole context, so pass every value you want kept. Reconciles the
                  app so the running pods pick it up.
  --clear-security-context
                  Remove the security context entirely, returning the app to the
                  platform default. This is how a --run-as-non-root=false opt-out
                  is revoked once the image no longer needs root.
`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mode, _ := cmd.Flags().GetString("mode")
		storage, _ := cmd.Flags().GetString("storage")
		displayName, _ := cmd.Flags().GetString("display-name")
		// --slug is set-if-changed so `--slug ""` explicitly clears the override
		// (reverting to the derived host), distinct from omitting the flag.
		slugChanged := cmd.Flags().Changed("slug")
		slug, _ := cmd.Flags().GetString("slug")
		// --database is set-if-changed too, so `--database ""` clears the binding
		// back to the default rather than being indistinguishable from omitting it.
		databaseChanged := cmd.Flags().Changed("database")
		database, _ := cmd.Flags().GetString("database")
		// instead of being indistinguishable from omitting the flag.
		var command, cmdArgs, releaseCommand *[]string
		if cmd.Flags().Changed("command") {
			v, _ := cmd.Flags().GetStringArray("command")
			command = &v
		}
		if cmd.Flags().Changed("arg") {
			v, _ := cmd.Flags().GetStringArray("arg")
			cmdArgs = &v
		}
		if cmd.Flags().Changed("release-command") {
			v, _ := cmd.Flags().GetStringArray("release-command")
			releaseCommand = &v
		}
		clearSecurityContext, _ := cmd.Flags().GetBool("clear-security-context")
		securityContext := securityContextFromFlags(cmd)
		if clearSecurityContext && securityContext != nil {
			return fmt.Errorf("--clear-security-context cannot be combined with the hardening flags")
		}
		securityContextChanged := clearSecurityContext || securityContext != nil

		if mode == "" && storage == "" && displayName == "" && !slugChanged && !databaseChanged && command == nil && cmdArgs == nil && releaseCommand == nil && !securityContextChanged {
			return fmt.Errorf("nothing to update: pass --display-name, --slug, --database, --mode, --storage, --command, --arg, --release-command, the hardening flags and/or --clear-security-context")
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		c := getClient()
		appID, err := appIDFrom(c, cmd, args)
		if err != nil {
			return err
		}

		var app *client.App
		var updErr error
		action := func() {
			if displayName != "" {
				app, updErr = c.UpdateAppDisplayName(context.Background(), appID, displayName)
				if updErr != nil {
					return
				}
			}
			if slugChanged {
				app, updErr = c.UpdateAppURLSlug(context.Background(), appID, slug)
				if updErr != nil {
					return
				}
			}
			if databaseChanged {
				app, updErr = c.SetAppDatabase(context.Background(), appID, database)
				if updErr != nil {
					return
				}
			}
			if storage != "" {
				app, updErr = c.UpdateAppStorage(context.Background(), appID, storage)
				if updErr != nil {
					return
				}
			}
			if securityContextChanged {
				// nil clears it; securityContextFromFlags already returns nil when
				// no hardening flag was passed, which is exactly what --clear means.
				app, updErr = c.SetAppSecurityContext(context.Background(), appID, securityContext)
				if updErr != nil {
					return
				}
			}
			if command != nil || cmdArgs != nil || releaseCommand != nil {
				app, updErr = c.UpdateAppCommand(context.Background(), appID, command, cmdArgs, releaseCommand)
				if updErr != nil {
					return
				}
			}
			if mode != "" {
				app, updErr = c.SwitchMode(context.Background(), appID, mode)
			}
		}

		if !isStructured(outputFormat) {
			withSpinner("Updating app...", action)
		} else {
			action()
		}
		if updErr != nil {
			return updErr
		}

		if isStructured(outputFormat) {
			return renderData(app)
		}

		storageDisplay := mutedStyle.Render("none")
		if app.Storage != "" {
			path := app.StoragePath
			if path == "" {
				path = "/data"
			}
			storageDisplay = fmt.Sprintf("%s at %s", app.Storage, path)
		}
		fmt.Println(renderInfoBox("App Updated", [][]string{
			{"ID", mutedStyle.Render(app.ID)},
			{"Name", app.Name},
			{"Display Name", app.DisplayName},
			{"URL Slug", slugDisplay(app.URLSlug)},
			{"Mode", app.Mode},
			{"Storage", storageDisplay},
			{"Status", renderStatus(app.Status)},
			{"URL", lipgloss.NewStyle().Bold(true).Foreground(colorInfo).Render(app.URL)},
		}))
		return nil
	},
}

var appVersionCmd = &cobra.Command{
	Use:   "version [name]",
	Short: "Show the release an app is running",
	Long: "Prints what is live: the release a deploy published, the image behind it " +
		"(digest-pinned), and the deploy that put it there.\n\n" +
		"A deploy made without --release has no release name; it is identified by its " +
		"deployment id instead.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		appID, err := appIDFrom(c, cmd, args)
		if err != nil {
			return err
		}
		v, err := c.GetAppVersion(context.Background(), appID)
		if err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(v)
		}

		deployed := mutedStyle.Render("—")
		if v.DeployedAt != nil {
			deployed = *v.DeployedAt
			if t, parseErr := parseTimeAgo(*v.DeployedAt); parseErr == nil {
				deployed = t
			}
		}
		rows := [][]string{
			{"App", v.AppName},
			{"Release", releaseDisplay(v.Release)},
			{"Image", v.Image},
		}
		if v.ResolvedImage != "" && v.ResolvedImage != v.Image {
			rows = append(rows, []string{"Digest", mutedStyle.Render(v.ResolvedImage)})
		}
		rows = append(rows,
			[]string{"Status", renderStatus(v.Status)},
			[]string{"Deployed", deployed},
		)
		if v.Trigger != "" {
			rows = append(rows, []string{"Trigger", v.Trigger})
		}
		if v.DeployedBy != "" {
			rows = append(rows, []string{"By", mutedStyle.Render(v.DeployedBy)})
		}
		if v.CommitSHA != "" {
			rows = append(rows, []string{"Commit", mutedStyle.Render(v.CommitSHA)})
		}
		if len(v.ReleaseCommand) > 0 {
			rows = append(rows, []string{"Release command", strings.Join(v.ReleaseCommand, " ")})
		}
		if v.DeploymentID != "" {
			rows = append(rows, []string{"Deployment", mutedStyle.Render(v.DeploymentID)})
		}
		fmt.Println(renderInfoBox("Version", rows))
		return nil
	},
}

// releaseDisplay renders a release name, marking the unreleased case rather than
// printing a blank cell.
func releaseDisplay(release string) string {
	if release == "" {
		return mutedStyle.Render("(unreleased)")
	}
	return lipgloss.NewStyle().Bold(true).Render(release)
}

var appRollbackCmd = &cobra.Command{
	Use:   "rollback [name] [release|prev]",
	Short: "Roll an app back to a previous release",
	Long: "Re-deploys the exact image a previous release ran — its digest, not its tag, " +
		"so the rollback reproduces the build that was tested.\n\n" +
		"Pass a release name, or 'prev' (the default) for the release before the current " +
		"one. Releases come from `app deploy --release`; `app deployments` lists them.\n\n" +
		"The release command is not re-run and no migration is reversed. If the rollback " +
		"would move the app back past a release command, it asks for confirmation first " +
		"(--yes to skip the prompt).",
	Args: cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		// The app can come from --app or the first positional; the release target is
		// then whichever positional is left over.
		appArgs, target := splitRollbackArgs(cmd, args)

		outputFormat := rootCmd.Flag("output").Value.String()
		c := getClient()
		appID, err := appIDFrom(c, cmd, appArgs)
		if err != nil {
			return err
		}

		req := client.RollbackRequest{Release: target, ConfirmMigrations: yes}
		var app *client.App
		var rollbackErr error
		action := func() { app, rollbackErr = c.RollbackApp(context.Background(), appID, req) }

		if !isStructured(outputFormat) {
			withSpinner("Rolling back...", action)
		} else {
			action()
		}

		// The server refuses a rollback that crosses a migration until it is
		// acknowledged; show what it said and let the user decide here rather than
		// making them re-run the command.
		if crossesMigrations(rollbackErr) && !isStructured(outputFormat) {
			fmt.Println(warnBox.Render(rollbackErr.Error()))
			ok, err := confirm("Roll back anyway?", "", "Yes, roll back")
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
			req.ConfirmMigrations = true
			withSpinner("Rolling back...", action)
		}
		if rollbackErr != nil {
			return rollbackErr
		}

		if isStructured(outputFormat) {
			return renderData(app)
		}

		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Rolled back %s to %s", app.Name, releaseDisplay(app.Release)),
		))
		fmt.Println()
		fmt.Println(renderInfoBox("Rollback", [][]string{
			{"Name", app.Name},
			{"Release", releaseDisplay(app.Release)},
			{"Image", app.Image},
			{"Status", renderStatus(app.Status)},
			{"URL", lipgloss.NewStyle().Bold(true).Foreground(colorInfo).Render(app.URL)},
		}))
		return nil
	},
}

// splitRollbackArgs separates the app reference from the release target in
// `rollback [name] [release|prev]`. The app is never implicit, so without --app
// the first positional names it and the second is the target; with --app every
// positional is the target. No target means the previous release.
func splitRollbackArgs(cmd *cobra.Command, args []string) ([]string, string) {
	if cmd.Flags().Changed("app") {
		if len(args) > 0 {
			return nil, args[0]
		}
		return nil, ""
	}
	if len(args) >= 2 {
		return args[:1], args[1]
	}
	return args, ""
}

// crossesMigrations reports whether the API refused a rollback because it would
// move the app back past a release command.
func crossesMigrations(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.Code == "MIGRATION_CONFIRMATION_REQUIRED"
}

var appTrafficCmd = &cobra.Command{
	Use:   "traffic <name>",
	Short: "Show current traffic split for an app",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		appID, err := appIDFrom(c, cmd, args)
		if err != nil {
			return err
		}
		targets, err := c.GetTraffic(context.Background(), appID)
		if err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(targets)
		}

		rows := make([][]string, len(targets))
		for i, t := range targets {
			rows[i] = []string{t.Revision, fmt.Sprintf("%d%%", t.Percent), t.URL}
		}
		render([]string{"REVISION", "PERCENT", "URL"}, rows, targets)
		return nil
	},
}

var appTrafficSetCmd = &cobra.Command{
	Use:   "set <name>",
	Short: "Set traffic split for an app",
	Long: `Set traffic split for an app. Specify revision=percent pairs with --revision flags,
or use --to-latest to route 100% traffic to the latest revision.

Examples:
  fpcloud app traffic set <name> --revision rev1=90 --revision rev2=10
  fpcloud app traffic set <name> --to-latest`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		toLatest, _ := cmd.Flags().GetBool("to-latest")
		revisionFlags, _ := cmd.Flags().GetStringArray("revision")

		var targets []client.TrafficTarget

		if toLatest {
			targets = []client.TrafficTarget{
				{Revision: "@latest", Percent: 100},
			}
		} else {
			if len(revisionFlags) == 0 {
				return fmt.Errorf("specify --revision flags or --to-latest")
			}
			for _, rv := range revisionFlags {
				parts := strings.SplitN(rv, "=", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid revision format %q, expected name=percent", rv)
				}
				var pct int
				if _, err := fmt.Sscanf(parts[1], "%d", &pct); err != nil {
					return fmt.Errorf("invalid percent %q in revision %q", parts[1], rv)
				}
				targets = append(targets, client.TrafficTarget{
					Revision: parts[0],
					Percent:  int64(pct),
				})
			}
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		c := getClient()
		appID, err := appIDFrom(c, cmd, args)
		if err != nil {
			return err
		}

		var result []client.TrafficTarget
		var setErr error
		action := func() {
			result, setErr = c.SetTraffic(context.Background(), appID, targets)
		}

		if !isStructured(outputFormat) {
			withSpinner("Setting traffic...", action)
		} else {
			action()
		}
		if setErr != nil {
			return setErr
		}

		if isStructured(outputFormat) {
			return renderData(result)
		}

		rows := make([][]string, len(result))
		for i, t := range result {
			rows[i] = []string{t.Revision, fmt.Sprintf("%d%%", t.Percent), t.URL}
		}
		render([]string{"REVISION", "PERCENT", "URL"}, rows, result)
		return nil
	},
}

var appDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an app",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ref := appRefFrom(cmd, args)
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			ok, err := confirm(
				fmt.Sprintf("Delete app %q?", ref),
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
		appID, err := appIDFrom(c, cmd, args)
		if err != nil {
			return err
		}
		if err := c.DeleteApp(context.Background(), appID); err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" App %q deleted.", ref),
		))
		return nil
	},
}

var appIdentityCmd = &cobra.Command{
	Use:   "identity <name>",
	Short: "Show workload identity for an app",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		appID, err := appIDFrom(c, cmd, args)
		if err != nil {
			return err
		}
		app, err := c.GetApp(context.Background(), appID)
		if err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(map[string]string{
				"app_id":             app.ID,
				"app_name":           app.Name,
				"service_account_id": app.ServiceAccountID,
			})
		}

		if app.ServiceAccountID == "" {
			fmt.Println(mutedStyle.Render("No workload identity attached to this app."))
			fmt.Println(mutedStyle.Render("Use --service-account when creating an app to attach one."))
			return nil
		}

		fmt.Println(renderInfoBox("Workload Identity", [][]string{
			{"App", app.Name},
			{"Service Account", app.ServiceAccountID},
		}))
		return nil
	},
}

var appDeploymentsCmd = &cobra.Command{
	Use:   "deployments <name>",
	Short: "List deployment history for an app",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		appID, err := appIDFrom(c, cmd, args)
		if err != nil {
			return err
		}
		deployments, err := c.ListDeployments(context.Background(), appID)
		if err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(deployments)
		}

		rows := make([][]string, len(deployments))
		for i, d := range deployments {
			// Truncate ID to first 12 chars.
			id := d.ID
			if len(id) > 12 {
				id = id[:12]
			}

			// Format duration.
			duration := "\u2014" // em dash
			if d.DurationMs != nil {
				ms := *d.DurationMs
				if ms < 1000 {
					duration = fmt.Sprintf("%dms", ms)
				} else if ms < 60000 {
					duration = fmt.Sprintf("%ds", ms/1000)
				} else {
					duration = fmt.Sprintf("%dm %ds", ms/60000, (ms%60000)/1000)
				}
			}

			// Format relative time.
			created := d.CreatedAt
			if t, parseErr := parseTimeAgo(d.CreatedAt); parseErr == nil {
				created = t
			}

			// Render status with appropriate style.
			status := renderDeploymentStatus(d.Status)

			rows[i] = []string{id, releaseCell(d.Release), d.Image, status, duration, created}
		}
		render([]string{"ID", "RELEASE", "IMAGE", "STATUS", "DURATION", "CREATED"}, rows, deployments)
		return nil
	},
}

// releaseCell renders a deployment's release name in a table, marking a deploy
// that carried none rather than leaving the column blank.
func releaseCell(release string) string {
	if release == "" {
		return mutedStyle.Render("—")
	}
	return release
}

// parseTimeAgo converts an RFC3339 time string to a relative time like "2m ago".
func parseTimeAgo(ts string) (string, error) {
	layouts := []string{
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05.999999999Z07:00",
	}
	for _, layout := range layouts {
		if t, err := timeParse(layout, ts); err == nil {
			d := timeNow().Sub(t)
			switch {
			case d < time.Minute:
				return fmt.Sprintf("%ds ago", int(d.Seconds())), nil
			case d < time.Hour:
				return fmt.Sprintf("%dm ago", int(d.Minutes())), nil
			case d < 24*time.Hour:
				return fmt.Sprintf("%dh ago", int(d.Hours())), nil
			default:
				return fmt.Sprintf("%dd ago", int(d.Hours()/24)), nil
			}
		}
	}
	return "", fmt.Errorf("cannot parse time: %s", ts)
}

// renderDeploymentStatus renders a deployment status with color.
func renderDeploymentStatus(status string) string {
	switch status {
	case "active":
		return renderStatus("active")
	case "deploying", "building":
		return renderStatus("deploying")
	case "failed":
		return renderStatus("failed")
	case "superseded":
		return lipgloss.NewStyle().Foreground(colorMuted).Render("\u25cb " + status)
	case "pending":
		return renderStatus("pending")
	default:
		return status
	}
}

var appLogsCmd = &cobra.Command{
	Use:   "logs <name>",
	Short: "Read app logs",
	Long: `Read an app's logs.

Without --follow the lines come from the platform's log store, so they reach
past the pod that printed them — a restart, a redeploy or a scale-to-zero no
longer erases the history — and every replica appears in one timeline.

  fpcloud app logs web --since 24h
  fpcloud app logs web --since 2h --until 1h --timestamps
  fpcloud app logs web --follow

--follow streams the running pod instead, which is where a line appears first.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		follow, _ := cmd.Flags().GetBool("follow")
		tail, _ := cmd.Flags().GetInt("tail")
		since, _ := cmd.Flags().GetString("since")
		until, _ := cmd.Flags().GetString("until")
		timestamps, _ := cmd.Flags().GetBool("timestamps")

		if follow && (since != "" || until != "") {
			return fmt.Errorf("--since/--until read stored history and --follow streams the live pod; pick one")
		}

		c := getClient()
		appID, err := appIDFrom(c, cmd, args)
		if err != nil {
			return err
		}
		body, err := c.GetAppLogs(context.Background(), appID, client.LogsRequest{
			Follow: follow, Tail: tail, Since: since, Until: until, Timestamps: timestamps,
		})
		if err != nil {
			return err
		}
		defer body.Close()

		_, err = io.Copy(os.Stdout, body)
		return err
	},
}

func init() {
	appCreateCmd.Flags().String("image", "", "Container image")
	appCreateCmd.Flags().Int("port", 8080, "Container port (default 8080, use 80 for nginx)")
	appCreateCmd.Flags().Int("replicas", 1, "Number of replicas")
	appCreateCmd.Flags().String("ingress", "internal", "Ingress setting: 'all' (public) or 'internal' (default)")
	appCreateCmd.Flags().StringArray("route", nil, "Keep a path prefix off the public ingress while it stays reachable in-cluster: 'path[:visibility]' (e.g. /internal/). Repeatable; needs --ingress all")
	appCreateCmd.Flags().String("mode", "always-on", "Hosting mode: 'always-on' (default) or 'serverless' (scale-to-zero)")
	appCreateCmd.Flags().String("type", "web", "Process type: 'web' (default, serves HTTP) or 'worker' (long-running process with no port, URL or health checks). Frozen at create")
	appCreateCmd.Flags().String("storage", "", "Attach a persistent volume of this size (e.g. 50Gi). Always-on mode only; opt-in")
	appCreateCmd.Flags().String("storage-path", "/data", "Mount path for the persistent volume (default: /data)")
	appCreateCmd.Flags().String("service-account", "", "Service account email for workload identity")
	appCreateCmd.Flags().StringArray("command", nil, "Override the container entrypoint (repeatable; empty = image ENTRYPOINT)")
	appCreateCmd.Flags().StringArray("arg", nil, "Container argument (repeatable; empty = image CMD), e.g. --arg -in-cluster")
	appCreateCmd.Flags().StringArray("release-command", nil, "Command run once per deploy before the new version goes live, e.g. \"npm run migrate\" (single string runs via sh -c; repeat for exec form)")
	appCreateCmd.Flags().String("display-name", "", "Cosmetic display name (defaults to the app name); mutable later via `app update --display-name`")
	appCreateCmd.Flags().String("slug", "", "Optional vanity URL slug; the app is reachable at <slug>.app.<platform-domain> instead of the derived host (always-on apps; globally unique)")
	appCreateCmd.Flags().StringArray("mount", nil, "Mount a ConfigMap/Secret/emptyDir (repeatable), e.g. --mount configmap:my-config:/etc/app[:sub-path] or --mount emptydir::/tmp")
	appCreateCmd.Flags().Int64("run-as-user", -1, "Run the container as this UID (hardening; -1 = image default)")
	appCreateCmd.Flags().Int64("run-as-group", -1, "Run the container as this GID (hardening; -1 = image default)")
	appCreateCmd.Flags().Int64("fs-group", -1, "fsGroup owning mounted volumes (hardening; -1 = unset)")
	appCreateCmd.Flags().Bool("run-as-non-root", false, "Require the container not run as UID 0 (hardening)")
	appUpdateCmd.Flags().Int64("run-as-user", -1, "Run the container as this UID (-1 = leave to the image)")
	appUpdateCmd.Flags().Int64("run-as-group", -1, "Run the container as this GID (-1 = leave to the image)")
	appUpdateCmd.Flags().Int64("fs-group", -1, "Own mounted volumes as this GID (-1 = leave to the image)")
	appUpdateCmd.Flags().Bool("run-as-non-root", false, "Require the container not run as UID 0")
	appUpdateCmd.Flags().Bool("read-only-root-fs", false, "Mount the container root filesystem read-only")
	appUpdateCmd.Flags().Bool("clear-security-context", false, "Remove the security context, returning the app to the platform default")

	appCreateCmd.Flags().Bool("read-only-root-fs", false, "Mount the container root filesystem read-only (hardening; pair with --mount emptydir::/tmp for scratch)")
	appCreateCmd.Flags().String("health-check-path", "/", "Health check HTTP path (default: /)")
	appCreateCmd.Flags().Int("health-check-timeout", 5, "Health check timeout in seconds")
	appCreateCmd.Flags().Int("health-check-interval", 10, "Health check interval in seconds")
	appCreateCmd.Flags().Int("health-check-retries", 3, "Health check failure threshold")
	registerProbeFlags(appCreateCmd)

	appListCmd.Flags().Bool("show-id", false, "Include the app ID column in the output")

	appDeployCmd.Flags().String("image", "", "Container image (required)")
	appDeployCmd.Flags().Bool("no-traffic", false, "Deploy without routing traffic to the new revision")
	appDeployCmd.Flags().String("release", "", "Name this release (e.g. v1.4.0) — reported by `app version` and targetable by `app rollback`")

	appReconcileCmd.Flags().Bool("all", false, "Reconcile every app in the current project")
	appSetRoutesCmd.Flags().StringArray("route", nil, "Path prefix to carve out: 'path[:visibility]' (visibility defaults to internal). Repeatable")
	appSetRoutesCmd.Flags().Bool("clear", false, "Remove every route carve-out, putting all paths back under the app-wide ingress")

	registerProbeFlags(appSetProbesCmd)
	appSetProbesCmd.Flags().Bool("clear", false, "Drop every per-probe override, putting all three probes back on the shared health check")

	appTrafficSetCmd.Flags().StringArray("revision", nil, "Revision traffic target (name=percent)")
	appTrafficSetCmd.Flags().Bool("to-latest", false, "Route 100% traffic to the latest revision")
	appTrafficCmd.AddCommand(appTrafficSetCmd)

	appDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	appLogsCmd.Flags().BoolP("follow", "f", false, "Stream the running pod live instead of reading stored history")
	appLogsCmd.Flags().Int("tail", 0, "Number of most recent lines to show (default 100, server-bounded)")
	appLogsCmd.Flags().String("since", "", "Read from this far back: a duration ago (e.g. 24h) or an RFC3339 timestamp (default: as far as the store retains)")
	appLogsCmd.Flags().String("until", "", "Read up to this point: a duration ago (e.g. 1h) or an RFC3339 timestamp (default: now)")
	appLogsCmd.Flags().Bool("timestamps", false, "Prefix each line with when it was printed")

	appScaleCmd.Flags().Int32("min", 0, "Minimum replicas — serverless is always 0 (scale-to-zero)")
	appScaleCmd.Flags().Int32("max", 10, "Maximum number of replicas (serverless mode)")
	appScaleCmd.Flags().Int32("replicas", 1, "Fixed replica count (always-on mode)")
	appScaleCmd.Flags().String("cpu", "500m", "CPU limit (e.g. 500m, 1)")
	appScaleCmd.Flags().String("memory", "512Mi", "Memory limit (e.g. 512Mi, 1Gi)")

	appRollbackCmd.Flags().BoolP("yes", "y", false, "Roll back without confirming when the rollback crosses a release command")

	appUpdateCmd.Flags().String("display-name", "", "New cosmetic display name (the frozen app name is unchanged)")
	appUpdateCmd.Flags().String("slug", "", "Set the vanity URL slug (<slug>.app.<platform-domain>); pass --slug \"\" to clear it back to the derived host (always-on apps)")
	appUpdateCmd.Flags().String("database", "", "Database this app's DATABASE_URL points at (name or id); pass --database \"\" to clear it back to the project's sole database")
	appUpdateCmd.Flags().String("mode", "", "New hosting mode: 'always-on' or 'serverless'")
	appUpdateCmd.Flags().String("storage", "", "Grow the persistent volume to this size (e.g. 100Gi). Grow-only, always-on mode")
	appUpdateCmd.Flags().StringArray("command", nil, "Override the container entrypoint (repeatable; pass with no value to clear back to the image ENTRYPOINT)")
	appUpdateCmd.Flags().StringArray("arg", nil, "Container argument (repeatable; pass with no value to clear back to the image CMD)")
	appUpdateCmd.Flags().StringArray("release-command", nil, "Command run once per deploy before the new version goes live, e.g. \"npm run migrate\" (single string runs via sh -c; pass with no value to drop the release phase)")

	// --app is a persistent flag on the app command tree: every app-scoped
	// subcommand accepts `--app <name|id>`, with the positional <name> as a
	// shorthand (the flag wins when both are given).
	appCmd.PersistentFlags().String("app", "", "App name or ID (the positional <name> is a shorthand)")

	appCmd.AddCommand(appCreateCmd, appListCmd, appGetCmd, appDeployCmd, appReconcileCmd, appUpdateCmd, appDeleteCmd, appLogsCmd, appRevisionsCmd, appScaleCmd, appSetRoutesCmd, appSetProbesCmd, appRollbackCmd, appVersionCmd, appIdentityCmd, appTrafficCmd, appDeploymentsCmd)
	rootCmd.AddCommand(appCmd)
}
