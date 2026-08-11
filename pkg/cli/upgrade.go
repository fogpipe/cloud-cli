package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

// upgradeRepo is the PUBLIC distribution repo the release workflow publishes
// binaries to (release-fpcloud.yml's DIST_REPO). It used to name the private
// monorepo, which no tenant can see — anyone who installed via the documented
// curl|sh, Homebrew or Nix path got an undocumented `gh` requirement and then a
// 404 on a repo they have no access to (#558). Being public also means the
// assets are plain HTTPS downloads, so no GitHub CLI or login is involved and
// upgrade works inside a container or CI image.
//
// The target version is the latest release published to that repo — never the
// control plane's build version (GET /version). The API is deployed per merge
// and the CLI is released per tag, so the control plane routinely runs a version
// that was never published as a CLI release; taking it as the upgrade target
// nagged about, and then 404'd on, a tag that does not exist (#781).
const upgradeRepo = "fogpipe/cloud-cli"

// nixStorePrefix is where Nix keeps installed binaries. The store is read-only
// by design, so a Nix-installed fpcloud can never replace itself — upgrade
// explains the Nix update path instead of failing on a permission error.
const nixStorePrefix = "/nix/store/"

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Download and install the latest fpcloud release",
	Long: "Replace the running fpcloud binary in place with the latest release\n" +
		"published to " + upgradeRepo + ", fetched over HTTPS.\n\n" +
		"When fpcloud was installed with Nix, the binary is immutable and upgrade\n" +
		"prints the Nix update path instead of replacing anything.",
	RunE: func(cmd *cobra.Command, args []string) error {
		exePath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate current binary: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
			exePath = resolved
		}

		if strings.HasPrefix(exePath, nixStorePrefix) {
			return nixUpgradeNotice()
		}

		latest, err := latestReleaseVersion()
		if err != nil {
			return fmt.Errorf("couldn't determine the latest fpcloud release from %s: %w\n"+
				"  Releases: https://github.com/%s/releases", upgradeRepo, err, upgradeRepo)
		}

		if isUpToDate(version, latest) {
			fmt.Printf("Already up to date (%s).\n", version)
			return nil
		}

		asset := fmt.Sprintf("fpcloud-%s-%s", runtime.GOOS, runtime.GOARCH)
		// Download to the same directory so the final rename is atomic (same filesystem).
		tmp := filepath.Join(filepath.Dir(exePath), ".fpcloud-upgrade-tmp")

		fmt.Printf("Downloading %s %s...\n", asset, latest)
		if err := downloadReleaseAsset(latest, asset, tmp); err != nil {
			_ = os.Remove(tmp)
			return err
		}

		if err := os.Chmod(tmp, 0o755); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("chmod: %w", err)
		}
		if err := os.Rename(tmp, exePath); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("install to %s (need write permission?): %w", exePath, err)
		}

		fmt.Printf("Upgraded fpcloud %s → %s (%s)\n", version, latest, exePath)
		return nil
	},
}

func isUpToDate(current, latest string) bool {
	return current != "dev" && semver.IsValid(current) && semver.IsValid(latest) && semver.Compare(current, latest) >= 0
}

// nixUpgradeNotice explains how to update a Nix-installed fpcloud. The version
// check is best-effort: it makes the message concrete when the release feed is
// reachable, but an unreachable GitHub must not hide the instructions.
func nixUpgradeNotice() error {
	latest, err := latestReleaseVersion()
	if err == nil && isUpToDate(version, latest) {
		fmt.Printf("Already up to date (%s). Installed with Nix — updates come from your flake, not `fpcloud upgrade`.\n", version)
		return nil
	}

	versions := fmt.Sprintf("You have %s", version)
	if err == nil {
		versions += fmt.Sprintf("; the latest release is %s", latest)
	}

	return fmt.Errorf("this fpcloud was installed with Nix, so it can't replace its own binary (the Nix store is read-only).\n"+
		"  %s. Update it through Nix instead:\n"+
		"    nix flake update fpcloud     # a flake input — then re-enter the dev shell\n"+
		"    nix profile upgrade fpcloud  # an ad-hoc profile install\n"+
		"  Details: https://github.com/fogpipe/cloud-cli", versions)
}

func init() {
	rootCmd.AddCommand(upgradeCmd)
}

// releaseCheckTimeout bounds the "what is the latest release?" lookup. It sits in
// front of `upgrade` and (once a day) every other command, so a slow or blocked
// github.com must not hang the CLI.
const releaseCheckTimeout = 3 * time.Second

// fetchLatestReleaseVersion resolves the newest release tag in the public
// distribution repo by following the /releases/latest redirect, which points at
// /releases/tag/<tag>. That is a plain unauthenticated HTTPS request to
// github.com rather than api.github.com, so it needs no token and is not subject
// to the 60-per-hour anonymous API rate limit that a shared CI egress IP burns
// through. Drafts and prereleases are excluded by GitHub, so the tag returned is
// always one whose assets are downloadable.
func fetchLatestReleaseVersion() (string, error) {
	return latestReleaseTagFrom(fmt.Sprintf("https://github.com/%s/releases/latest", upgradeRepo))
}

// latestReleaseVersion is the seam the rest of the CLI resolves the upgrade
// target through, so tests can supply a version without reaching github.com.
var latestReleaseVersion = fetchLatestReleaseVersion

func latestReleaseTagFrom(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), releaseCheckTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	tag := path.Base(resp.Header.Get("Location"))
	if !semver.IsValid(tag) {
		// No releases yet (the redirect lands on /releases), or GitHub answered
		// with something other than a redirect.
		return "", fmt.Errorf("%s returned %s with no release tag", url, resp.Status)
	}
	return tag, nil
}

// downloadReleaseAsset fetches one release asset from the public distribution
// repo straight over HTTPS, into dst.
func downloadReleaseAsset(tag, asset, dst string) error {
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", upgradeRepo, tag, asset)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s returned %s", asset, url, resp.Status)
	}

	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", dst, err)
	}
	// Close reports write errors a buffered copy has not surfaced yet; a
	// truncated binary must not be installed over the working one.
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}
