package client

import (
	"fmt"
	"os"
	"strings"
)

// EnvAPIKey is the API key the environment names: FPCLOUD_API_KEY, else the
// contents of the file FPCLOUD_API_KEY_FILE points at. The platform mounts an
// app's workload identity key as that file (fogpipe/cloud-workspace#1106), so
// fpcloud and the provider running inside an app authenticate as its service
// account with nothing configured. Empty when neither is set; a named file that
// cannot be read is an error, never a fall-through to another credential.
func EnvAPIKey() (string, error) {
	if key := os.Getenv("FPCLOUD_API_KEY"); key != "" {
		return key, nil
	}
	path := os.Getenv("FPCLOUD_API_KEY_FILE")
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read FPCLOUD_API_KEY_FILE: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}
