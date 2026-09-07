package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fogpipe/cloud-cli/pkg/client"
	"gopkg.in/yaml.v3"
)

// Config holds the CLI configuration.
type Config struct {
	APIUrl                 string `yaml:"api_url,omitempty"`
	APIKey                 string `yaml:"api_key,omitempty"`
	CurrentOrg             string `yaml:"current_org,omitempty"`
	CurrentOrgFKE          bool   `yaml:"current_org_fke,omitempty"` // cached FKE entitlement of CurrentOrg; hides the `fke` command tree when false (server still enforces)
	CurrentProject         string `yaml:"current_project,omitempty"`
	SuppressVersionWarning bool   `yaml:"suppress_version_warning,omitempty"`
}

// stateDirName is what fpcloud calls its state directory, in $HOME and inside a
// project alike — there is one name, so there is one thing to look for.
const stateDirName = ".fpcloud"

// stateDir is where a state file the CLI writes goes: the nearest .fpcloud/ at
// or above the working directory, else ~/.fpcloud. `fpcloud init` creating one
// is therefore all it takes for a directory to keep its context — and its
// login — to itself, and two directories can hold two identities at once.
func stateDir() string {
	if dir := localStateDir(); dir != "" {
		return dir
	}
	return homeStateDir()
}

// statePath resolves one state file for reading: the nearest .fpcloud/ at or
// above the working directory that HOLDS IT, else ~/.fpcloud.
//
// The fallback is per file rather than per directory, which is the whole reason
// there is no second variable to know about: a project dir holding only
// config.yaml gets its own org and project and goes on using your global login,
// with nothing to configure and no re-auth per repo.
func statePath(name string) string {
	dir, err := os.Getwd()
	if err == nil {
		for {
			p := filepath.Join(dir, stateDirName, name)
			if _, err := os.Stat(p); err == nil {
				return p
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return filepath.Join(homeStateDir(), name)
}

// localStateDir is the nearest .fpcloud/ directory at or above the working
// directory, or "" when there is none.
func localStateDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, stateDirName)
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func homeStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, stateDirName)
}

func configPath() string {
	return statePath("config.yaml")
}

func loadConfig() (*Config, error) {
	cfg := &Config{}
	data, err := os.ReadFile(configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}

func saveConfig(cfg *Config) error {
	dir := stateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// getClient builds an API client from the resolved API URL and credential.
func getClient() *client.Client {
	return newClient(resolveAPIURL(), resolveAPIKey())
}

// resolveAPIURL picks the control plane every command talks to: an explicit
// --api-url, then FPCLOUD_API_URL, then the api_url in config.yaml, then the
// built-in default (prod for a release, localhost for a `dev` build) — the
// last two both reached through the flag's default. Same order as the
// Terraform provider, and as resolveAPIKey.
//
// Every surface that names the API resolves through here, `fpcloud context`
// included: a CLI that printed one control plane and called another would be
// worse than one that ignored the variable outright.
func resolveAPIURL() string {
	flag := rootCmd.Flag("api-url")
	if flag.Changed {
		return flag.Value.String()
	}
	if url := os.Getenv("FPCLOUD_API_URL"); url != "" {
		return url
	}
	return flag.Value.String()
}

// resolveAPIKey picks the credential every API call carries, in the same order
// the Terraform provider uses so one variable means one thing whichever binary
// reads it: an explicit --api-key, then FPCLOUD_API_KEY (what OIDC federation
// mints in CI, and what the registry path has always honoured), then the key
// stored in config.yaml, then the OIDC token from `fpcloud login`
// — the same identity kubectl uses, so interactive use needs no separate key
// (gcloud-style). Returns "" when nothing authenticates the caller.
func resolveAPIKey() string {
	flag := rootCmd.Flag("api-key")
	if flag.Changed {
		return flag.Value.String()
	}
	if key := os.Getenv("FPCLOUD_API_KEY"); key != "" {
		return key
	}
	if key := flag.Value.String(); key != "" {
		return key
	}
	if token, err := currentIDToken(); err == nil {
		return token
	}
	return ""
}

// newClient builds an API client that reports this binary's version. pkg/client
// reads its own module version out of the caller's build info, which is empty
// for the binary that module ships itself — so the CLI is the one caller that
// has to state it, from the same ldflags-injected value `fpcloud version`
// prints.
func newClient(apiURL, cred string) *client.Client {
	c := client.New(apiURL, cred)
	c.Version = version
	return c
}

// requireProject returns the current project, failing if none is set.
func requireProject() (string, error) {
	project := rootCmd.Flag("project").Value.String()
	if project == "" {
		return "", fmt.Errorf("no project specified; use --project flag or `fpcloud switch`")
	}
	return project, nil
}
