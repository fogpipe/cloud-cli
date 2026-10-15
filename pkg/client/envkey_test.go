package client

import "testing"

func TestEnvAPIKey(t *testing.T) {
	t.Setenv("FPCLOUD_API_KEY", "")
	if got := EnvAPIKey(); got != "" {
		t.Fatalf("nothing set: got %q", got)
	}
	t.Setenv("FPCLOUD_API_KEY", "fp-var")
	if got := EnvAPIKey(); got != "fp-var" {
		t.Fatalf("got %q, want fp-var", got)
	}
}
