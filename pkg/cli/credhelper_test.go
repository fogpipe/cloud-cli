package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIsDockerCredentialHelper(t *testing.T) {
	cases := map[string]bool{
		"/usr/local/bin/docker-credential-fpcloud": true,
		"docker-credential-fpcloud":                true,
		"/home/x/bin/fpcloud":                      false,
		"fpcloud":                                  false,
	}
	for argv0, want := range cases {
		if got := isDockerCredentialHelper(argv0); got != want {
			t.Errorf("isDockerCredentialHelper(%q) = %v, want %v", argv0, got, want)
		}
	}
}

func TestRunDockerCredentialHelperGetUnknownHost(t *testing.T) {
	// A host we don't serve must report "not found" (exit 1) without touching the
	// cluster — Docker reads that as anonymous.
	r, w, _ := os.Pipe()
	_, _ = w.WriteString("registry.example.com\n")
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()

	if code := runDockerCredentialHelper([]string{"get"}); code != 1 {
		t.Fatalf("get unknown host: exit = %d, want 1", code)
	}
}

func TestAddDockerCredHelpersPreservesExisting(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".docker")
	_ = os.MkdirAll(cfgDir, 0o755)
	path := filepath.Join(cfgDir, "config.json")
	// Pre-existing config with an unrelated field + an existing helper.
	_ = os.WriteFile(path, []byte(`{"auths":{"x":{}},"credHelpers":{"other.io":"osxkeychain"}}`), 0o600)
	t.Setenv("DOCKER_CONFIG", cfgDir)

	if _, err := addDockerCredHelpers([]string{registryHost}); err != nil {
		t.Fatal(err)
	}

	var cfg map[string]json.RawMessage
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg["auths"]; !ok {
		t.Error("auths field was dropped")
	}
	var helpers map[string]string
	_ = json.Unmarshal(cfg["credHelpers"], &helpers)
	if helpers["other.io"] != "osxkeychain" {
		t.Error("existing credHelper was dropped")
	}
	if helpers[registryHost] != dockerCredHelperName {
		t.Errorf("registry helper = %q, want %q", helpers[registryHost], dockerCredHelperName)
	}
}

// A `credHelpers` entry naming a helper that is not installed hijacks the
// registry: docker consults a helper before `auths`, so storing a token beside
// the entry changes nothing and the push goes on failing with an error that
// names neither (fogpipe/cloud-workspace#1042). The stored-token path removes
// the entry for that reason, and only ours.
func TestRemoveDockerCredHelpersDropsOnlyOurs(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".docker")
	_ = os.MkdirAll(cfgDir, 0o755)
	path := filepath.Join(cfgDir, "config.json")
	_ = os.WriteFile(path, []byte(`{"auths":{"x":{}},"credHelpers":{"other.io":"osxkeychain","`+registryHost+`":"`+dockerCredHelperName+`"}}`), 0o600)
	t.Setenv("DOCKER_CONFIG", cfgDir)

	if _, err := removeDockerCredHelpers([]string{registryHost}); err != nil {
		t.Fatal(err)
	}

	var cfg map[string]json.RawMessage
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg["auths"]; !ok {
		t.Error("auths field was dropped — the token this path just stored lives there")
	}
	var helpers map[string]string
	_ = json.Unmarshal(cfg["credHelpers"], &helpers)
	if _, ok := helpers[registryHost]; ok {
		t.Errorf("our entry survived: %v", helpers)
	}
	if helpers["other.io"] != "osxkeychain" {
		t.Errorf("someone else's entry was dropped: %v", helpers)
	}
}

// Installing the link is half the job; docker finds it by name on PATH. A
// prefix that takes the symlink and is not on PATH answers `executable file not
// found in $PATH` at the next push, which is the same failure as a prefix that
// refused the symlink — so both have to be an error here rather than a success
// this command reports in a colour.
func TestEnsureDockerCredentialHelperRefusesAPrefixOffPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	link, err := ensureDockerCredentialHelper()
	if err == nil {
		t.Fatalf("installed %s and reported success, with nothing on PATH to find it by", link)
	}
}
