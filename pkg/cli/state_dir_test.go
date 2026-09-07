package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// tempRoot is t.TempDir() with symlinks resolved, so a path built from it
// compares equal to what os.Getwd reports after chdir into it.
func tempRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// isolateState points HOME and the working directory at a fresh tree holding a
// .fpcloud/, so a test reads and writes its own state rather than whatever the
// developer running it is logged into. It returns that directory.
func isolateState(t *testing.T) string {
	t.Helper()
	root := tempRoot(t)
	home := filepath.Join(root, "home")
	wd := filepath.Join(root, "work")
	local := filepath.Join(wd, ".fpcloud")
	for _, d := range []string{filepath.Join(home, ".fpcloud"), local} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Chdir(wd)
	return local
}

// isolateHomeState is the same isolation for a directory that keeps no state of
// its own: everything must resolve to ~/.fpcloud. It returns the home one.
func isolateHomeState(t *testing.T) string {
	t.Helper()
	root := tempRoot(t)
	home := filepath.Join(root, "home")
	wd := filepath.Join(root, "work", "deep", "deeper")
	global := filepath.Join(home, ".fpcloud")
	for _, d := range []string{global, wd} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Chdir(wd)
	return global
}

// With no .fpcloud/ anywhere above the working directory, every file is the
// global one — which is what an install that has never run `fpcloud init` is.
func TestStateResolvesToHomeWithoutALocalDir(t *testing.T) {
	global := isolateHomeState(t)
	for _, tc := range []struct{ name, got string }{
		{"config.yaml", configPath()},
		{"oidc-token.json", tokenCachePath()},
		{"version-check.json", versionCachePath()},
	} {
		if want := filepath.Join(global, tc.name); tc.got != want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, want)
		}
	}
}

// The walk starts at the working directory and stops at the first .fpcloud/,
// so a state dir at the root of a checkout covers every directory under it.
func TestStateDirIsFoundByWalkingUp(t *testing.T) {
	root := tempRoot(t)
	repo := filepath.Join(root, "repo")
	deep := filepath.Join(repo, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".fpcloud"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Chdir(deep)

	if got, want := stateDir(), filepath.Join(repo, ".fpcloud"); got != want {
		t.Errorf("stateDir() = %q, want %q", got, want)
	}
}

// The nearest one wins, so a directory can hold an identity of its own inside a
// checkout that already holds one.
func TestNearestStateDirWins(t *testing.T) {
	root := tempRoot(t)
	inner := filepath.Join(root, "repo", "inner")
	if err := os.MkdirAll(filepath.Join(inner, ".fpcloud"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "repo", ".fpcloud"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Chdir(inner)

	if got, want := stateDir(), filepath.Join(inner, ".fpcloud"); got != want {
		t.Errorf("stateDir() = %q, want %q", got, want)
	}
}

// The fallback is per FILE. A directory holding only config.yaml has its own
// org and project and goes on using the global login — the whole reason there
// is one concept here rather than two variables to know the difference between.
func TestFallbackToHomeIsPerFile(t *testing.T) {
	local := isolateState(t)
	home := filepath.Join(os.Getenv("HOME"), ".fpcloud")
	if err := os.WriteFile(filepath.Join(local, "config.yaml"), []byte("current_org: acme\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "oidc-token.json"), []byte(`{"id_token":"global"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if got, want := configPath(), filepath.Join(local, "config.yaml"); got != want {
		t.Errorf("config = %q, want the local one %q", got, want)
	}
	if got, want := tokenCachePath(), filepath.Join(home, "oidc-token.json"); got != want {
		t.Errorf("token = %q, want the global one %q", got, want)
	}
}

// A login below a local state dir writes its token there even though the file
// does not exist yet — otherwise `fpcloud init` followed by `fpcloud login`
// would authenticate every other directory on the machine as well, which is the
// case this whole rule exists for.
func TestWritesGoToTheLocalDirEvenWhenTheFileResolvesGlobally(t *testing.T) {
	local := isolateState(t)
	home := filepath.Join(os.Getenv("HOME"), ".fpcloud")
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("current_org: global\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got, want := configPath(), filepath.Join(home, "config.yaml"); got != want {
		t.Fatalf("config should still read globally until one is written here: %q", got)
	}
	if err := saveConfig(&Config{CurrentOrg: "acme", CurrentProject: "demo"}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "config.yaml")); err != nil {
		t.Fatalf("config.yaml was not written to the local state dir: %v", err)
	}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.CurrentOrg != "acme" || cfg.CurrentProject != "demo" {
		t.Errorf("the local config did not take effect: %+v", cfg)
	}
}

// Every file the CLI keeps follows the same rule — there is no per-file
// exception to remember, which is what made the old two-variable split
// unreadable.
func TestEveryStateFileFollowsTheLocalDir(t *testing.T) {
	local := isolateState(t)
	for _, tc := range []struct{ name, got string }{
		{"config.yaml", filepath.Join(stateDir(), "config.yaml")},
		{"oidc-token.json", filepath.Join(stateDir(), "oidc-token.json")},
		{"version-check.json", filepath.Join(stateDir(), "version-check.json")},
		{filepath.Join("storage", "b.json"), filepath.Join(storageCredsWriteDir(), "b.json")},
	} {
		if want := filepath.Join(local, tc.name); tc.got != want {
			t.Errorf("%s writes to %q, want %q", tc.name, tc.got, want)
		}
	}
}

// `fpcloud init` creates the directory the rule looks for, and says so rather
// than failing when it is already there.
func TestInitCreatesTheLocalStateDir(t *testing.T) {
	root := tempRoot(t)
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Chdir(root)

	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	fi, err := os.Stat(filepath.Join(root, ".fpcloud"))
	if err != nil || !fi.IsDir() {
		t.Fatalf(".fpcloud/ was not created: %v", err)
	}
	if got, want := stateDir(), filepath.Join(root, ".fpcloud"); got != want {
		t.Errorf("stateDir() = %q, want %q", got, want)
	}
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("init on an existing dir: %v", err)
	}
}
