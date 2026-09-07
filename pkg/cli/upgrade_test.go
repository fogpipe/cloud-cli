package cli

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsUpToDate(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.76.0", "v0.76.0", true},
		{"v0.77.0", "v0.76.0", true},
		{"v0.75.0", "v0.76.0", false},
		{"dev", "v0.76.0", false},
		{"v0.76.0-dirty", "v0.76.0", false},
	}
	for _, c := range cases {
		if got := isUpToDate(c.current, c.latest); got != c.want {
			t.Errorf("isUpToDate(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

// A Nix-installed fpcloud must never attempt a self-replace: the store is
// read-only, so the download would fail with an opaque permission error.
func TestNixNoticeExplainsTheNixPath(t *testing.T) {
	restore := stubLatestRelease(t, "", errUnreachable)
	defer restore()

	err := nixUpgradeNotice()
	if err == nil {
		t.Fatal("want an error when the version is unknown, got nil")
	}
	for _, want := range []string{"installed with Nix", "nix flake update", "nix profile upgrade"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("notice missing %q:\n%s", want, err)
		}
	}
}

func TestNixStorePrefixMatchesRealStorePaths(t *testing.T) {
	if !strings.HasPrefix("/nix/store/abc123-fpcloud-0.76.0/bin/fpcloud", nixStorePrefix) {
		t.Error("nixStorePrefix does not match a store path")
	}
	if strings.HasPrefix("/home/me/bin/fpcloud", nixStorePrefix) {
		t.Error("nixStorePrefix matches a plain install path")
	}
}

// errUnreachable stands in for "github.com could not be reached".
var errUnreachable = errors.New("unreachable")

func stubLatestRelease(t *testing.T, tag string, err error) func() {
	t.Helper()
	prev := latestReleaseVersion
	latestReleaseVersion = func() (string, error) { return tag, err }
	return func() { latestReleaseVersion = prev }
}

// The upgrade target is what the platform says is installable on every surface,
// never the version the control plane happens to be serving. The two differ for
// a stretch after every release — the provider's registry ingestion is the slow
// one — and taking the second moves the cli past the provider that carries the
// same version (fogpipe/cloud-workspace#813).
func TestLatestReleaseIsThePlatformsInstallableVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"version":"v0.3.0","min_client_version":"v0.1.0","latest_release":"v0.2.0"}`)
	}))
	defer srv.Close()
	t.Setenv("FPCLOUD_API_URL", srv.URL)

	got, err := fetchLatestRelease()
	if err != nil {
		t.Fatalf("fetchLatestRelease: %v", err)
	}
	if got != "v0.2.0" {
		t.Errorf("got %q, want v0.2.0 — the installable version, not the deployed one", got)
	}
}

// A deployment that cannot currently name an installable release omits the
// field. That is an error rather than a fall back to `version`: the whole point
// of the field is that the deployed version is not always installable, so
// substituting it is the failure this exists to prevent.
func TestLatestReleaseRefusesToSubstituteTheDeployedVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"version":"v0.3.0","min_client_version":"v0.1.0"}`)
	}))
	defer srv.Close()
	t.Setenv("FPCLOUD_API_URL", srv.URL)

	if got, err := fetchLatestRelease(); err == nil {
		t.Errorf("want an error when the platform names no installable release, got %q", got)
	}
}
