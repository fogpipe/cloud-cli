package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runSegment runs the emitted snippet under /bin/sh FROM a working directory
// and returns what prompt_segment printed. The snippet discovers its config the
// way the CLI does — by walking up from $PWD — so where it runs is the input.
// HOME is planted alongside, or the snippet would read the config of whoever is
// running the tests.
func runSegment(t *testing.T, script, wd string, extra ...string) string {
	t.Helper()
	home := filepath.Join(filepath.Dir(wd), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", script+"\nprompt_segment")
	cmd.Dir = wd
	cmd.Env = append(os.Environ(), append([]string{"HOME=" + home, "PWD=" + wd}, extra...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running snippet: %v\n%s", err, out)
	}
	return string(out)
}

func renderSegment(t *testing.T, dir string) string {
	t.Helper()
	return runSegment(t, promptSegmentScript("bash"), dir)
}

// strip removes the ANSI colouring so a test asserts on the text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// writePromptConfig returns a working directory whose own .fpcloud/ holds the
// config — the arrangement `fpcloud init` makes.
func writePromptConfig(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(tempRoot(t), "repo")
	if err := os.MkdirAll(filepath.Join(dir, ".fpcloud"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".fpcloud", "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPromptSegmentRendersOrgAndProject(t *testing.T) {
	dir := writePromptConfig(t, "api_url: https://api.example\ncurrent_org: acme\ncurrent_project: web\n")
	if got := stripANSI(renderSegment(t, dir)); got != "fp acme:web" {
		t.Fatalf("got %q, want %q", got, "fp acme:web")
	}
}

// A missing half still renders, so "you have an org but no project" is visible
// rather than indistinguishable from having no context at all.
func TestPromptSegmentRendersPartialContext(t *testing.T) {
	dir := writePromptConfig(t, "current_org: acme\n")
	if got := stripANSI(renderSegment(t, dir)); got != "fp acme:-" {
		t.Fatalf("got %q, want %q", got, "fp acme:-")
	}
}

func TestPromptSegmentIsEmptyWithoutContext(t *testing.T) {
	dir := writePromptConfig(t, "api_url: https://api.example\n")
	if got := renderSegment(t, dir); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestPromptSegmentIsEmptyWithoutConfigFile(t *testing.T) {
	dir := filepath.Join(tempRoot(t), "repo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := renderSegment(t, dir); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// A quoted value is a value, not part of the name shown.
func TestPromptSegmentUnquotesValues(t *testing.T) {
	dir := writePromptConfig(t, "current_org: \"acme\"\ncurrent_project: 'web'\n")
	if got := stripANSI(renderSegment(t, dir)); got != "fp acme:web" {
		t.Fatalf("got %q, want %q", got, "fp acme:web")
	}
}

// The zsh variant colours with prompt escapes, not raw ANSI: dropped into
// RPROMPT, an escape sequence zsh has not been told to skip is counted as
// printable and the prompt is drawn at the wrong width.
func TestPromptSegmentZshUsesPromptEscapes(t *testing.T) {
	dir := writePromptConfig(t, "current_org: acme\ncurrent_project: web\n")
	out := runSegment(t, promptSegmentScript("zsh"), dir)
	if got, want := string(out), "%F{244}fp%f %F{37}acme%f%F{244}:%f%F{208}web%f"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if strings.ContainsRune(string(out), 0x1b) {
		t.Fatalf("zsh variant emitted a raw escape sequence: %q", out)
	}
}

func TestPromptSegmentHonoursSymbolOverride(t *testing.T) {
	dir := writePromptConfig(t, "current_org: acme\ncurrent_project: web\n")
	out := runSegment(t, promptSegmentScript("bash"), dir, "FPCLOUD_PROMPT_SYMBOL=☁")
	if got := stripANSI(string(out)); got != "☁ acme:web" {
		t.Fatalf("got %q, want %q", got, "☁ acme:web")
	}
}
