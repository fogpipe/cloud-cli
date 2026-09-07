package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

// versionCmd prints the CLI and control-plane versions, kubectl-style.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show the fpcloud CLI and control-plane versions",
	RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("suppress") {
			suppress, _ := cmd.Flags().GetBool("suppress")
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			cfg.SuppressVersionWarning = suppress
			if err := saveConfig(cfg); err != nil {
				return err
			}
			if suppress {
				fmt.Println("Upgrade notices suppressed. Re-enable with `fpcloud version --suppress=false`.")
			} else {
				fmt.Println("Upgrade notices re-enabled.")
			}
		}

		fmt.Printf("Client Version: %s\n", version)
		if clientOnly, _ := cmd.Flags().GetBool("client"); clientOnly {
			return nil
		}
		apiURL := resolveAPIURL()
		server, err := fetchServerVersion(apiURL)
		if err != nil {
			fmt.Printf("Server Version: unreachable (%s)\n", apiURL)
			return nil
		}
		fmt.Printf("Server Version: %s\n", server.Version)
		if server.MinClientVersion == "" {
			return nil
		}
		fmt.Printf("Minimum Client Version: %s\n", server.MinClientVersion)
		// The number to pin. It is the whole product's — the same version has a
		// downloadable binary and a provider the OpenTofu registry serves — so
		// it is what a stack pinning both should name, and it is regularly
		// below Server Version while the registry catches up.
		if server.LatestRelease != "" {
			fmt.Printf("Latest Release: %s\n", server.LatestRelease)
		}
		if semver.IsValid(version) && semver.IsValid(server.MinClientVersion) &&
			semver.Compare(version, server.MinClientVersion) < 0 {
			fmt.Printf("\n%s\n", lipgloss.NewStyle().Bold(true).Foreground(colorWarning).Render(
				"This deployment refuses fpcloud "+version+". Run `fpcloud upgrade`."))
		}
		return nil
	},
}

func init() {
	versionCmd.Flags().Bool("suppress", false, "Suppress the 'new version available' notice on every command")
	versionCmd.Flags().Bool("client", false, "Show only the client version, without contacting the control plane")
	rootCmd.AddCommand(versionCmd)
}

func hasVersionFlag(args []string) bool {
	for _, a := range args {
		if a == "--version" || a == "-v" || strings.HasPrefix(a, "--version=") {
			return true
		}
	}
	return false
}

// versionCheckTTL is how long a fetched "latest version" is trusted before the
// CLI looks the releases up again — keeps the warning cheap (one network call
// per day, otherwise a local file read).
const versionCheckTTL = 24 * time.Hour

type versionCache struct {
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
}

func versionCachePath() string {
	return statePath("version-check.json")
}

// serverVersion is what GET /version reports: the deployment's own build
// version, and the oldest client it will answer.
type serverVersion struct {
	Version          string `json:"version"`
	MinClientVersion string `json:"min_client_version"`
	// DeployedAt is when the control plane's version first began serving, by
	// the fleet's account; empty from a deployment that does not say.
	DeployedAt string `json:"deployed_at,omitempty"`
	// LatestRelease is the newest version of the product installable on every
	// surface — this binary and the OpenTofu provider alike. Empty when the
	// platform cannot currently say, which is not the same as nothing being
	// installable and is never filled in with Version.
	LatestRelease string `json:"latest_release,omitempty"`
}

// fetchServerVersion asks the control plane for its build version (GET /version,
// unauthenticated). Short timeout so it never noticeably blocks a command.
func fetchServerVersion(apiURL string) (serverVersion, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var body serverVersion
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/version", nil)
	if err != nil {
		return body, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return body, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return body, fmt.Errorf("version endpoint returned %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return body, err
	}
	if body.Version == "" {
		return body, fmt.Errorf("empty version")
	}
	return body, nil
}

// latestVersion returns the newest version of the product that is installable,
// using the on-disk cache when fresh and otherwise asking the control plane.
//
// Deliberately neither of the two numbers this has been in the past. Not the
// control plane's own `version`, which is a tag the API is serving and says
// nothing about anything having been published — warning about a version nobody
// can install, and then 404ing on it, is what that produced (#781). And not
// github's newest cli release either, which is right about this binary and
// silent about the OpenTofu provider that carries the same version, so it moves
// half of a tenant's install (fogpipe/cloud-workspace#813).
func latestVersion() (string, error) {
	if data, err := os.ReadFile(versionCachePath()); err == nil {
		var c versionCache
		if json.Unmarshal(data, &c) == nil && c.Latest != "" && time.Since(c.CheckedAt) < versionCheckTTL {
			return c.Latest, nil
		}
	}

	latest, err := latestReleaseVersion()
	if err != nil {
		return "", err
	}

	if data, err := json.Marshal(versionCache{Latest: latest, CheckedAt: time.Now()}); err == nil {
		dir := stateDir()
		_ = os.MkdirAll(dir, 0o700)
		_ = os.WriteFile(filepath.Join(dir, "version-check.json"), data, 0o600)
	}
	return latest, nil
}

// warnIfOutdated prints a one-line upgrade hint to stderr when the running CLI is
// older than the newest published release. It never errors or blocks meaningful
// work: any failure (offline, dev build, unparseable version) is silently skipped.
//
// Only a build stamped with a bare release tag is judged. `git describe` decorates
// a local build with the commits since that tag (v0.134.0-3-gf9054f2, -dirty), and
// semver orders that below the tag it is built past — so the release it is ahead of
// would read as an upgrade, and `fpcloud upgrade` would install older code.
func warnIfOutdated() {
	if version == "dev" || !semver.IsValid(version) ||
		semver.Prerelease(version) != "" || semver.Build(version) != "" {
		return
	}
	if cfg, err := loadConfig(); err == nil && cfg.SuppressVersionWarning {
		return
	}
	latest, err := latestVersion()
	if err != nil || !semver.IsValid(latest) {
		return
	}
	if semver.Compare(version, latest) < 0 {
		fmt.Fprintf(os.Stderr,
			"⚠ fpcloud %s is available (you have %s). Run `fpcloud upgrade`.\n",
			latest, version)
	}
}
