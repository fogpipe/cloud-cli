package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// render runs the emitted snippet under /bin/sh against a config dir and
// returns what prompt_segment printed. The snippet is shipped as shell, so the
// only test that means anything is one that executes it.
func renderSegment(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", promptSegmentScript("bash")+"\nprompt_segment")
	cmd.Env = append(os.Environ(), "FPCLOUD_CONFIG_DIR="+dir, "FPCLOUD_STATE_DIR="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running snippet: %v\n%s", err, out)
	}
	return string(out)
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

func writePromptConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
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
	if got := renderSegment(t, t.TempDir()); got != "" {
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
	cmd := exec.Command("/bin/sh", "-c", promptSegmentScript("zsh")+"\nprompt_segment")
	cmd.Env = append(os.Environ(), "FPCLOUD_CONFIG_DIR="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running snippet: %v\n%s", err, out)
	}
	if got, want := string(out), "%F{244}fp%f %F{37}acme%f%F{244}:%f%F{208}web%f"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if strings.ContainsRune(string(out), 0x1b) {
		t.Fatalf("zsh variant emitted a raw escape sequence: %q", out)
	}
}

func TestPromptSegmentHonoursSymbolOverride(t *testing.T) {
	dir := writePromptConfig(t, "current_org: acme\ncurrent_project: web\n")
	cmd := exec.Command("/bin/sh", "-c", promptSegmentScript("bash")+"\nprompt_segment")
	cmd.Env = append(os.Environ(), "FPCLOUD_CONFIG_DIR="+dir, "FPCLOUD_PROMPT_SYMBOL=☁")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running snippet: %v\n%s", err, out)
	}
	if got := stripANSI(string(out)); got != "☁ acme:web" {
		t.Fatalf("got %q, want %q", got, "☁ acme:web")
	}
}
