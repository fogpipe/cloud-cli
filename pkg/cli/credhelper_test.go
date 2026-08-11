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
