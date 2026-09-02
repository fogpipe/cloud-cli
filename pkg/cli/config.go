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

// stateDir is the fpcloud state dir (~/.fpcloud by default). It holds per-account,
// non-project state — the cached OIDC token, the version-check stamp, the storage
// key cache. FPCLOUD_STATE_DIR relocates it (gcloud's CLOUDSDK_CONFIG), so a repo
// can keep every byte of fpcloud state inside the project dir; FPCLOUD_CONFIG_DIR
// alone deliberately does not, so a per-directory config still reuses your global
// login instead of forcing a re-auth per directory.
func stateDir() string {
	if dir := os.Getenv("FPCLOUD_STATE_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".fpcloud")
}

// configDir is where config.yaml lives. FPCLOUD_CONFIG_DIR overrides it so
// org/project context can be scoped per-directory (e.g. via direnv) without
// moving the login; otherwise it follows stateDir().
func configDir() string {
	if dir := os.Getenv("FPCLOUD_CONFIG_DIR"); dir != "" {
		return dir
	}
	return stateDir()
}

func configPath() string {
	return filepath.Join(configDir(), "config.yaml")
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
	dir := configDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(configPath(), data, 0o600); err != nil {
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
// stored in config.yaml, then the Google OIDC token from `fpcloud auth login`
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
