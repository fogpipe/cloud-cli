package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "What the control plane is serving, and since when",
	Long: `Report the platform as a tenant sees it: which control-plane version is
serving, when that version was deployed, the oldest client it still answers,
and whether this client clears it.

The deployed-at time is the answer to "did the platform change under me". A
cap that starts to bind, a reconcile that rewrites a quota, a default that
moves — each arrives with a release, and the release is the one event the
platform can name (fogpipe/cloud-workspace#144). Resource-level diagnosis
stays where it is: ` + "`fpcloud project status`" + ` and each resource's own
show command.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		apiURL := resolveAPIURL()
		server, err := fetchServerVersion(apiURL)
		if err != nil {
			return fmt.Errorf("the control plane at %s did not answer: %w", apiURL, err)
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(platformStatus{
				ControlPlane: server.Version, DeployedAt: server.DeployedAt,
				MinClientVersion: server.MinClientVersion, Client: version,
			})
		}
		fmt.Print(renderPlatformStatus(server, version, time.Now()))
		if semver.IsValid(version) && semver.IsValid(server.MinClientVersion) &&
			semver.Compare(version, server.MinClientVersion) < 0 {
			fmt.Fprintln(os.Stderr, lipgloss.NewStyle().Bold(true).Foreground(colorWarning).Render(
				"This client is older than the oldest version the control plane serves; run `fpcloud upgrade`."))
		}
		return nil
	},
}

type platformStatus struct {
	ControlPlane     string `json:"control_plane" yaml:"control_plane"`
	DeployedAt       string `json:"deployed_at,omitempty" yaml:"deployed_at,omitempty"`
	MinClientVersion string `json:"min_client_version" yaml:"min_client_version"`
	Client           string `json:"client" yaml:"client"`
}

// renderPlatformStatus lays the answer out. The deploy time is shown both as
// written and as an age, because "66 minutes ago" is the reading that ends a
// hunt and the timestamp is the one that goes in a report; a control plane
// that does not say when it was deployed says so rather than showing a blank
// that reads as "never".
func renderPlatformStatus(server serverVersion, client string, now time.Time) string {
	deployed := mutedStyle.Render("not reported by this control plane")
	if server.DeployedAt != "" {
		deployed = server.DeployedAt
		if t, err := time.Parse(time.RFC3339, server.DeployedAt); err == nil {
			deployed = fmt.Sprintf("%s (%s ago)", t.UTC().Format(time.RFC3339), humanDuration(now.Sub(t)))
		}
	}
	return renderInfoBox("Platform", [][]string{
		{"Control plane", server.Version},
		{"Deployed", deployed},
		{"Oldest client served", server.MinClientVersion},
		{"This client", client},
	}) + "\n"
}

// humanDuration is coarse on purpose: the reader is placing a change in the
// day, not timing it.
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
