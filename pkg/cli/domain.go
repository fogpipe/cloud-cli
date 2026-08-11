package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

var domainCmd = &cobra.Command{
	Use:     "domain",
	Aliases: []string{"domains"},
	Short:   "Manage custom domains",
}

var domainAddCmd = &cobra.Command{
	Use:   "create <domain>",
	Short: "Add a custom domain to an app",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appRef, _ := cmd.Flags().GetString("app")
		if appRef == "" {
			return fmt.Errorf("--app is required")
		}

		mode, _ := cmd.Flags().GetString("mode")

		c := getClient()
		appID, err := resolveAppID(c, appRef)
		if err != nil {
			return err
		}
		d, err := c.AddDomain(context.Background(), appID, args[0], mode)
		if err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()

		// Edge mode (ADR-044) has no ownership/pointing/cert flow — TLS is
		// terminated upstream. Skip the verification breakdown; just confirm the
		// route and print the CNAME the upstream edge should point at.
		if d.Mode == client.DomainModeEdge {
			if isStructured(outputFormat) {
				return renderData(d)
			}
			fmt.Println(renderInfoBox("Custom Domain Added (edge)", [][]string{
				{"Domain", d.Domain},
				{"Mode", d.Mode},
				{"Status", renderStatus(d.Status)},
				{"TLS", "terminated upstream"},
			}))
			fmt.Println()
			fmt.Println(mutedStyle.Render("  Edge mode: fpcloud issues no certificate. Point your upstream edge"))
			fmt.Println(mutedStyle.Render("  (Cloudflare-for-SaaS, reverse proxy) at this app's origin; fpcloud"))
			fmt.Println(mutedStyle.Render("  routes the Host to your app over plain HTTP."))
			fmt.Println()
			return nil
		}

		// Fetch the verification breakdown so we can print the exact DNS
		// records (TXT ownership + CNAME/A pointing) the user must configure.
		vr, verr := c.VerifyDomain(context.Background(), appID, d.Domain)

		if isStructured(outputFormat) {
			if verr == nil {
				return renderData(vr)
			}
			return renderData(d)
		}

		status := d.Status
		if verr == nil && vr.Domain != nil {
			status = vr.Domain.Status
		}
		fmt.Println(renderInfoBox("Custom Domain Added", [][]string{
			{"Domain", d.Domain},
			{"Status", renderStatus(status)},
			{"TLS", renderStatus(d.TLSStatus)},
		}))
		fmt.Println()
		if verr == nil {
			renderDomainVerification(vr)
		} else {
			printDNSInstructions(d.Domain)
		}
		return nil
	},
}

var domainRemoveCmd = &cobra.Command{
	Use:     "delete <domain>",
	Aliases: []string{"rm"},
	Short:   "Remove a custom domain from an app",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appRef, _ := cmd.Flags().GetString("app")
		if appRef == "" {
			return fmt.Errorf("--app is required")
		}

		c := getClient()
		appID, err := resolveAppID(c, appRef)
		if err != nil {
			return err
		}
		if err := c.RemoveDomain(context.Background(), appID, args[0]); err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Domain %q removed from app %s.", args[0], appID),
		))
		return nil
	},
}

var domainListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List domains for an app",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		appRef, _ := cmd.Flags().GetString("app")
		if appRef == "" {
			return fmt.Errorf("--app is required")
		}

		c := getClient()
		appID, err := resolveAppID(c, appRef)
		if err != nil {
			return err
		}
		domains, err := c.ListDomains(context.Background(), appID)
		if err != nil {
			return err
		}

		rows := make([][]string, len(domains))
		for i, d := range domains {
			routes := "—"
			if len(d.Routes) > 0 {
				routes = fmt.Sprintf("%d apps", len(d.Routes)+1)
			}
			rows[i] = []string{d.Domain, d.Mode, renderStatus(d.Status), renderStatus(d.TLSStatus), routes}
		}
		render(
			[]string{"DOMAIN", "MODE", "STATUS", "TLS", "ROUTING"},
			rows,
			domains,
		)

		return nil
	},
}

var domainStatusCmd = &cobra.Command{
	Use:   "status <domain>",
	Short: "Show status and DNS instructions for a domain",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appRef, _ := cmd.Flags().GetString("app")
		if appRef == "" {
			return fmt.Errorf("--app is required")
		}

		c := getClient()
		appID, err := resolveAppID(c, appRef)
		if err != nil {
			return err
		}

		// The verify endpoint re-checks ownership + pointing live and advances
		// status; it returns the full breakdown and records still needed.
		vr, err := c.VerifyDomain(context.Background(), appID, args[0])
		if err != nil {
			return err
		}
		d := vr.Domain

		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(vr)
		}

		fmt.Println(renderInfoBox("Domain Status", [][]string{
			{"Domain", d.Domain},
			{"Status", renderStatus(d.Status)},
			{"TLS", renderStatus(d.TLSStatus)},
		}))
		fmt.Println()
		if d.Status == "active" {
			fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("  DNS is configured and the domain is active."))
			fmt.Println()
		} else {
			renderDomainVerification(vr)
		}
		return nil
	},
}

// renderDomainVerification prints the ownership/pointing/cert breakdown plus
// the exact DNS records the user still needs to configure.
func renderDomainVerification(vr *client.DomainVerification) {
	check := func(ok bool) string {
		if ok {
			return lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓")
		}
		return lipgloss.NewStyle().Bold(true).Foreground(colorDanger).Render("✗")
	}

	fmt.Println(lipgloss.NewStyle().Bold(true).Render("  Verification:"))
	fmt.Printf("    %s Domain ownership (TXT)\n", check(vr.TXTVerified))
	fmt.Printf("    %s DNS pointing (%s)\n", check(vr.DNSPointing), vr.PointingType)
	certLine := "TLS certificate"
	if !vr.CertReady && vr.CertReason != "" {
		certLine += " (" + vr.CertReason + ")"
	}
	if vr.CertReady && vr.CertExpiry != "" {
		certLine += " — expires " + vr.CertExpiry
	}
	fmt.Printf("    %s %s\n", check(vr.CertReady), certLine)
	fmt.Println()

	isWildcard := vr.Domain != nil && vr.Domain.Mode == client.DomainModeWildcard
	renderDomainRoutes(vr.Domain)

	if vr.TXTVerified && vr.DNSPointing && !isWildcard {
		fmt.Println(mutedStyle.Render("  DNS verified. TLS certificate will be issued automatically."))
		fmt.Println()
		return
	}

	fmt.Println(lipgloss.NewStyle().Bold(true).Render("  Add these DNS records:"))
	fmt.Println()
	if !vr.TXTVerified {
		fmt.Println(mutedStyle.Render("  Ownership (TXT):"))
		fmt.Printf("    %s  TXT  → %q\n", vr.TXTRecordName, vr.TXTRecordValue)
		fmt.Println()
	}
	if !vr.DNSPointing {
		fmt.Println(mutedStyle.Render(fmt.Sprintf("  Pointing (%s):", vr.PointingType)))
		fmt.Printf("    %s  %s  → %s\n", vr.PointingName, vr.PointingType, vr.PointingValue)
		fmt.Println()
	}
	// Wildcard mode needs no pointing record (DNSPointing is trivially true), but
	// does need this one-time ACME DNS-01 delegation — shown unconditionally since
	// fpcloud can't verify it live; cert-manager just retries silently until it's
	// there (#412).
	if isWildcard {
		fmt.Println(mutedStyle.Render("  ACME delegation (CNAME) — required once, before a wildcard cert can issue:"))
		fmt.Printf("    %s  CNAME  → %s\n", vr.AcmeCNAMEName, vr.AcmeCNAMEValue)
		fmt.Println()
	}
	fmt.Println(mutedStyle.Render("  A TLS certificate is issued only after all records above verify."))
	fmt.Println()
}

var domainSetRoutesCmd = &cobra.Command{
	Use:   "set-routes <domain>",
	Short: "Route path prefixes of a domain to other apps",
	Long: `Serve one hostname from several apps by path prefix (#581).

The domain's own app serves "/" — every path not claimed by a route. Each route
sends a more specific prefix to a different app in the same project, so a
frontend and an API can deploy independently on ONE origin instead of two (no
CORS, no cross-site cookies, no relocated OAuth callback).

The request path is not rewritten: an app mounted at /api/ receives /api/orders.

Replace-in-full — the routes given are the complete set.

  fpcloud domain set-routes shop.example.com --app web --route /api/=api
  fpcloud domain set-routes shop.example.com --app web --clear`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appRef, _ := cmd.Flags().GetString("app")
		if appRef == "" {
			return fmt.Errorf("--app is required")
		}
		routeFlags, _ := cmd.Flags().GetStringArray("route")
		clear, _ := cmd.Flags().GetBool("clear")
		if clear && len(routeFlags) > 0 {
			return fmt.Errorf("--clear cannot be combined with --route")
		}
		if !clear && len(routeFlags) == 0 {
			return fmt.Errorf("specify at least one --route, or --clear to remove all routes")
		}

		c := getClient()
		appID, err := resolveAppID(c, appRef)
		if err != nil {
			return err
		}
		routes, err := parseDomainRoutes(c, routeFlags)
		if err != nil {
			return err
		}

		domain, err := c.SetDomainRoutes(context.Background(), appID, args[0], routes)
		if err != nil {
			return err
		}
		if len(domain.Routes) == 0 {
			fmt.Println(successBox.Render(
				lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
					fmt.Sprintf(" Path routing cleared on %s — app %s serves the whole host.", domain.Domain, appRef),
			))
			return nil
		}
		rows := make([][]string, 0, len(domain.Routes)+1)
		for _, r := range domain.Routes {
			rows = append(rows, []string{r.Path, r.AppName})
		}
		rows = append(rows, []string{"/", appRef})
		render([]string{"PATH", "APP"}, rows, domain)
		return nil
	},
}

// parseDomainRoutes turns repeated --route "path=app" flags into a route table,
// resolving each backend app reference to its id.
func parseDomainRoutes(c *client.Client, flags []string) ([]client.DomainRoute, error) {
	routes := make([]client.DomainRoute, 0, len(flags))
	for _, f := range flags {
		path, appRef, ok := strings.Cut(f, "=")
		if !ok || strings.TrimSpace(path) == "" || strings.TrimSpace(appRef) == "" {
			return nil, fmt.Errorf("invalid --route %q (expected path=app, e.g. /api/=api)", f)
		}
		appID, err := resolveAppID(c, strings.TrimSpace(appRef))
		if err != nil {
			return nil, fmt.Errorf("route %q: %w", f, err)
		}
		routes = append(routes, client.DomainRoute{Path: strings.TrimSpace(path), AppID: appID})
	}
	return routes, nil
}

// renderDomainRoutes prints a domain's path fan-out, if it has one.
func renderDomainRoutes(d *client.Domain) {
	if d == nil || len(d.Routes) == 0 {
		return
	}
	fmt.Println(lipgloss.NewStyle().Bold(true).Render("  Path routing:"))
	fmt.Println()
	for _, r := range d.Routes {
		fmt.Printf("    %s  →  %s\n", r.Path, r.AppName)
	}
	fmt.Println(mutedStyle.Render("    /  →  this app (catch-all)"))
	fmt.Println()
}

// printDNSInstructions prints DNS setup guidance for a domain.
func printDNSInstructions(domain string) {
	fmt.Println(lipgloss.NewStyle().Bold(true).Render("  DNS Setup:"))
	fmt.Println()
	if strings.Contains(domain, ".") && !strings.HasPrefix(domain, "*.") {
		// Check if apex domain (no subdomain)
		parts := strings.Split(domain, ".")
		if len(parts) == 2 {
			// Apex domain (myapp.com)
			fmt.Println(mutedStyle.Render("  This is an apex domain. Add an A record:"))
			fmt.Println(fmt.Sprintf("    %s  A  → (your platform IP)", domain))
			fmt.Println(mutedStyle.Render("  Or use a CNAME-flattening provider (Cloudflare, etc.):"))
			fmt.Println(fmt.Sprintf("    %s  CNAME  → apps.cloud.fogpipe.com", domain))
		} else {
			// Subdomain (www.myapp.com, api.myapp.com)
			fmt.Println(mutedStyle.Render("  Add this CNAME record at your DNS provider:"))
			fmt.Println()
			fmt.Println(fmt.Sprintf("    %s  CNAME  → apps.cloud.fogpipe.com", domain))
		}
	}
	fmt.Println()
	fmt.Println(mutedStyle.Render("  TLS certificate will be issued automatically once DNS is configured."))
}

func init() {
	domainAddCmd.Flags().String("app", "", "App name or ID (required)")
	domainAddCmd.Flags().String("mode", "", "Attachment mode: verified (default), edge (TLS terminated upstream, no cert), on_demand (no TXT ownership proof, HTTP-01 cert on pointing), or wildcard (*.zone, DNS-01 cert)")
	domainRemoveCmd.Flags().String("app", "", "App name or ID (required)")
	domainListCmd.Flags().String("app", "", "App name or ID (required)")
	domainStatusCmd.Flags().String("app", "", "App name or ID (required)")
	domainSetRoutesCmd.Flags().String("app", "", "App name or ID owning the domain (required)")
	domainSetRoutesCmd.Flags().StringArray("route", nil, "Route a path prefix to another app: path=app (repeatable, e.g. /api/=api)")
	domainSetRoutesCmd.Flags().Bool("clear", false, "Remove all path routes; the domain's own app serves the whole host")

	domainCmd.AddCommand(domainAddCmd, domainRemoveCmd, domainListCmd, domainStatusCmd, domainSetRoutesCmd)
	rootCmd.AddCommand(domainCmd)
}
