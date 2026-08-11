package cli

import (
	"errors"
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

// The upgrade target is the newest *published* release, read off the
// /releases/latest redirect. The control plane's own version is deployed per
// merge and is regularly a tag that was never released, so it must not drive
// this (#781).
func TestLatestReleaseTagFollowsTheRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/fogpipe/cloud-cli/releases/tag/v0.112.1", http.StatusFound)
	}))
	defer srv.Close()

	got, err := latestReleaseTagFrom(srv.URL)
	if err != nil {
		t.Fatalf("latestReleaseTagFrom: %v", err)
	}
	if got != "v0.112.1" {
		t.Errorf("got %q, want v0.112.1", got)
	}
}

// A repo with no releases redirects to /releases, which carries no tag. That is
// an error, not an upgrade to a garbage version.
func TestLatestReleaseTagRejectsANonTagRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/fogpipe/cloud-cli/releases", http.StatusFound)
	}))
	defer srv.Close()

	if got, err := latestReleaseTagFrom(srv.URL); err == nil {
		t.Errorf("want an error for a tagless redirect, got %q", got)
	}
}
