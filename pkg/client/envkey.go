package client

import "os"

// EnvAPIKey is the API key the environment names, FPCLOUD_API_KEY, or empty.
// It is what OIDC federation mints in CI; an app's pods hold no key
// (ADR-283 §7).
func EnvAPIKey() string {
	return os.Getenv("FPCLOUD_API_KEY")
}
