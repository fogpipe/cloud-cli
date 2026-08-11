package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

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
		apiURL := cmd.Flag("api-url").Value.String()
		server, err := fetchServerVersion(apiURL)
		if err != nil {
			fmt.Printf("Server Version: unreachable (%s)\n", apiURL)
			return nil
		}
		fmt.Printf("Server Version: %s\n", server)
		return nil
	},
}

func init() {
	versionCmd.Flags().Bool("suppress", false, "Suppress the 'new version available' notice on every command")
	versionCmd.Flags().Bool("client", false, "Show only the client version, without contacting the control plane")
	rootCmd.AddCommand(versionCmd)
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
	// Global, per-account state — not per-project — so keep it in stateDir().
	return filepath.Join(stateDir(), "version-check.json")
}

// fetchServerVersion asks the control plane for its build version (GET /version,
// unauthenticated). Short timeout so it never noticeably blocks a command.
func fetchServerVersion(apiURL string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("version endpoint returned %d", resp.StatusCode)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Version == "" {
		return "", fmt.Errorf("empty version")
	}
	return body.Version, nil
}

// latestVersion returns the latest *released* CLI version, using the on-disk
// cache when fresh and otherwise refreshing it from the distribution repo.
//
// Deliberately not the control plane's version: the API deploys on every merge
// while the CLI is released per tag, so /version is regularly ahead of the newest
// published binary. Warning about a version nobody can install — and then having
// `upgrade` 404 on it — is exactly what that mismatch produced (#781).
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
		_ = os.MkdirAll(stateDir(), 0o700)
		_ = os.WriteFile(versionCachePath(), data, 0o600)
	}
	return latest, nil
}

// warnIfOutdated prints a one-line upgrade hint to stderr when the running CLI is
// older than the newest published release. It never errors or blocks meaningful
// work: any failure (offline, dev build, unparseable version) is silently skipped.
func warnIfOutdated() {
	if version == "dev" || !semver.IsValid(version) {
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
