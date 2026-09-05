package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// Docker names credential-helper binaries `docker-credential-<name>`, where <name>
// is the value stored under `credHelpers` in ~/.docker/config.json. `fpcloud auth
// configure-docker` registers fpcloud as that helper for the registry, symlinked so
// Docker can exec it.
const (
	dockerCredHelperPrefix = "docker-credential-"
	dockerCredHelperName   = "fpcloud"
)

// isDockerCredentialHelper reports whether fpcloud was invoked under its
// docker-credential-fpcloud alias (the symlink configure-docker installs).
func isDockerCredentialHelper(argv0 string) bool {
	return strings.HasPrefix(filepath.Base(argv0), dockerCredHelperPrefix)
}

// runDockerCredentialHelper implements the Docker credential-helper protocol
// (github.com/docker/docker-credential-helpers) so `docker push/pull` against the
// fpcloud registry present a fresh credential — the caller's own fpcloud identity
// (auto-refreshed ID token, or FPCLOUD_API_KEY), which the token broker
// exchanges for a scoped registry token — with no static `docker login`. It is
// invoked when fpcloud runs as docker-credential-fpcloud, bypassing cobra so stdout
// carries only protocol JSON. Returns the process exit code.
func runDockerCredentialHelper(args []string) int {
	action := ""
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "get":
		raw, _ := io.ReadAll(os.Stdin)
		host := strings.TrimSpace(string(raw))
		if host != registryHost {
			// Docker reads this exact message as "no credentials" (anonymous).
			fmt.Fprintln(os.Stderr, "credentials not found in native keychain")
			return 1
		}
		username, password, err := fetchRegistryCreds()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		out, _ := json.Marshal(map[string]string{
			"ServerURL": registryHost,
			"Username":  username,
			"Secret":    password,
		})
		fmt.Println(string(out))
		return 0
	case "store", "erase":
		// Credentials are fetched live; nothing to persist or remove. Drain stdin
		// (Docker writes the payload) and report success.
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	case "list":
		// No statically-stored credentials to enumerate.
		fmt.Println("{}")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "%s%s: unknown action %q\n", dockerCredHelperPrefix, dockerCredHelperName, action)
		return 1
	}
}

func dockerConfigPath() string {
	if d := os.Getenv("DOCKER_CONFIG"); d != "" {
		return filepath.Join(d, "config.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".docker", "config.json")
}

// addDockerCredHelpers merges `credHelpers[host] = fpcloud` into ~/.docker/config.json,
// preserving every other field (auths, credsStore, etc.) untouched.
func addDockerCredHelpers(hosts []string) (string, error) {
	path := dockerConfigPath()

	cfg := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return path, fmt.Errorf("parsing %s: %w", path, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return path, fmt.Errorf("reading %s: %w", path, err)
	}

	helpers := map[string]string{}
	if raw, ok := cfg["credHelpers"]; ok {
		_ = json.Unmarshal(raw, &helpers)
	}
	for _, h := range hosts {
		helpers[h] = dockerCredHelperName
	}
	raw, _ := json.Marshal(helpers)
	cfg["credHelpers"] = raw

	out, err := json.MarshalIndent(cfg, "", "\t")
	if err != nil {
		return path, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, err
	}
	return path, os.WriteFile(path, append(out, '\n'), 0o600)
}

// ensureDockerCredentialHelper installs a `docker-credential-fpcloud` symlink to the
// running fpcloud binary, in the binary's own directory (gcloud's model). Returns the
// symlink path. If the directory isn't writable (e.g. a read-only /nix/store), the
// caller falls back to printing a manual instruction.
func ensureDockerCredentialHelper() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	link := filepath.Join(filepath.Dir(exe), dockerCredHelperPrefix+dockerCredHelperName)
	if target, err := os.Readlink(link); err == nil && target == exe {
		return link, nil // already correct
	}
	_ = os.Remove(link) // replace a stale link/file
	if err := os.Symlink(exe, link); err != nil {
		return link, err
	}
	return link, nil
}

var authConfigureDockerCmd = &cobra.Command{
	Use:   "configure-docker [REGISTRY...]",
	Short: "Register fpcloud as a Docker credential helper for the fpcloud registry",
	Long: "Configure Docker to authenticate to the fpcloud container registry using your\n" +
		"browser login (gcloud-style). Adds a `credHelpers` entry to ~/.docker/config.json\n" +
		"so `docker push`/`pull` fetch a fresh password automatically — no `docker login`,\n" +
		"no static password on disk.\n\n" +
		"Defaults to " + registryHost + "; pass extra hosts to configure them too.",
	Example: "  fpcloud auth configure-docker",
	RunE: func(cmd *cobra.Command, args []string) error {
		hosts := args
		if len(hosts) == 0 {
			hosts = []string{registryHost}
		}

		link, linkErr := ensureDockerCredentialHelper()
		cfgPath, err := addDockerCredHelpers(hosts)
		if err != nil {
			return err
		}

		ok := lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓")
		fmt.Println(ok + " Docker configured to use fpcloud for:")
		for _, h := range hosts {
			fmt.Println(mutedStyle.Render("    " + h))
		}
		fmt.Println(mutedStyle.Render("  Updated " + cfgPath))
		if linkErr != nil {
			exe, _ := os.Executable()
			fmt.Println()
			fmt.Println(lipgloss.NewStyle().Foreground(colorWarning).Render(
				"  Could not install the docker-credential-fpcloud helper automatically:"))
			fmt.Println(mutedStyle.Render("    " + linkErr.Error()))
			fmt.Println(mutedStyle.Render("  Create it manually on your PATH (point it at the fpcloud binary), e.g.:"))
			fmt.Println(mutedStyle.Render(fmt.Sprintf("    ln -s %q ~/.local/bin/%s%s", exe, dockerCredHelperPrefix, dockerCredHelperName)))
		} else {
			fmt.Println(mutedStyle.Render("  Helper: " + link))
		}
		fmt.Println()
		fmt.Println(mutedStyle.Render("  Now: docker push " + registryHost + "/<org>/<project>/<app>:<tag>"))
		return nil
	},
}
