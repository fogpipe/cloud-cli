package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Docker names credential-helper binaries `docker-credential-<name>`, where <name>
// is the value stored under `credHelpers` in ~/.docker/config.json. `fpcloud
// registry login` registers fpcloud as that helper for the registry, symlinked so
// Docker can exec it.
const (
	dockerCredHelperPrefix = "docker-credential-"
	dockerCredHelperName   = "fpcloud"
)

// isDockerCredentialHelper reports whether fpcloud was invoked under its
// docker-credential-fpcloud alias (the symlink `registry login` installs).
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
	return editDockerCredHelpers(func(helpers map[string]string) {
		for _, h := range hosts {
			helpers[h] = dockerCredHelperName
		}
	})
}

// removeDockerCredHelpers drops our entry for each host, leaving anyone else's
// alone.
//
// An entry naming a helper that is not on PATH hijacks the registry: docker
// consults it before `auths`, so it cannot be worked around by logging in with
// a password, and it outlives the binary that wrote it
// (fogpipe/cloud-workspace#1042). So the fallback below removes the entry
// rather than leaving it beside the credential it just stored.
func removeDockerCredHelpers(hosts []string) (string, error) {
	return editDockerCredHelpers(func(helpers map[string]string) {
		for _, h := range hosts {
			if helpers[h] == dockerCredHelperName {
				delete(helpers, h)
			}
		}
	})
}

func editDockerCredHelpers(edit func(helpers map[string]string)) (string, error) {
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
	edit(helpers)
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
// running fpcloud binary, in the binary's own directory (gcloud's model), and returns
// the path docker will exec.
//
// It reports success only once the link is on PATH under the name docker looks
// it up by. Both halves are needed and only one used to be checked: an install
// prefix fpcloud cannot write to fails the symlink, and one that is not on PATH
// takes the symlink and is never found — and docker answers both with
// `exec: "docker-credential-fpcloud": executable file not found in $PATH`, long
// after the login that was supposed to configure it
// (fogpipe/cloud-workspace#1042).
func ensureDockerCredentialHelper() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	link := filepath.Join(filepath.Dir(exe), dockerCredHelperPrefix+dockerCredHelperName)
	if target, err := os.Readlink(link); err != nil || target != exe {
		_ = os.Remove(link) // replace a stale link/file
		if err := os.Symlink(exe, link); err != nil {
			return link, err
		}
	}
	found, err := exec.LookPath(dockerCredHelperPrefix + dockerCredHelperName)
	if err != nil {
		return link, fmt.Errorf("installed %s, which is not on PATH: %w", link, err)
	}
	if resolved, err := filepath.EvalSymlinks(found); err == nil {
		if want, err := filepath.EvalSymlinks(link); err == nil && resolved != want {
			return link, fmt.Errorf("installed %s, but %s on PATH resolves to %s", link, found, resolved)
		}
	}
	return link, nil
}
