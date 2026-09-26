package cli

import (
	"io"
	"os"
	"testing"
)

func stderrOf(t *testing.T, run func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	run()
	os.Stderr = old
	_ = w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestVersionFlagNotesTheLatestRelease(t *testing.T) {
	cases := []struct {
		name, running, latest, want string
	}{
		{"release behind is warned", "v0.189.0", "v0.190.1", "⚠ fpcloud v0.190.1 is available (you have v0.189.0). Run `fpcloud upgrade`.\n"},
		{"release current says nothing", "v0.190.1", "v0.190.1", ""},
		{"local build is told the latest release", "v0.189.0-5-gcf84515", "v0.190.1", "latest release: v0.190.1\n"},
		{"dirty local build is told the latest release", "v0.189.0-5-gcf84515-dirty", "v0.190.1", "latest release: v0.190.1\n"},
		{"dev build is told the latest release", "dev", "v0.190.1", "latest release: v0.190.1\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolateState(t)
			defer stubLatestRelease(t, c.latest, nil)()
			prev := version
			version = c.running
			defer func() { version = prev }()

			if got := stderrOf(t, noteLatestRelease); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestVersionFlagNoteIsSuppressible(t *testing.T) {
	isolateState(t)
	defer stubLatestRelease(t, "v0.190.1", nil)()
	if err := saveConfig(&Config{SuppressVersionWarning: true}); err != nil {
		t.Fatal(err)
	}
	prev := version
	version = "v0.189.0-5-gcf84515"
	defer func() { version = prev }()

	if got := stderrOf(t, noteLatestRelease); got != "" {
		t.Errorf("got %q, want nothing", got)
	}
}

func TestANixInstallIsNeverToldToUpgrade(t *testing.T) {
	isolateState(t)
	defer stubLatestRelease(t, "v0.190.1", nil)()
	prevExe := executable
	executable = func() (string, error) { return "/nix/store/abc-fpcloud-0.189.0/bin/fpcloud", nil }
	defer func() { executable = prevExe }()
	prev := version
	version = "v0.189.0"
	defer func() { version = prev }()

	if got := stderrOf(t, noteLatestRelease); got != "" {
		t.Errorf("got %q, want nothing", got)
	}
}
