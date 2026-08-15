package cli

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestGoogleExchangeGoesThroughTheBroker pins the reason this binary can be
// built from source at all: it carries no Google client secret, so the exchange
// has to leave for the platform rather than for accounts.google.com.
func TestGoogleExchangeGoesThroughTheBroker(t *testing.T) {
	if err := rootCmd.PersistentFlags().Set("api-url", "https://api.example.test"); err != nil {
		t.Fatal(err)
	}

	conf, err := oauthConfig(context.Background(), "http://127.0.0.1:1234")
	if err != nil {
		t.Fatalf("oauthConfig: %v", err)
	}
	if conf.Endpoint.TokenURL != "https://api.example.test"+brokerTokenPath {
		t.Errorf("token url = %q, want the broker", conf.Endpoint.TokenURL)
	}
	if conf.ClientSecret != "" {
		t.Error("the google client secret must never be present in this binary")
	}
	// The browser still goes to Google — only the back-channel is brokered.
	if !strings.Contains(conf.Endpoint.AuthURL, "accounts.google.com") {
		t.Errorf("auth url = %q, want Google", conf.Endpoint.AuthURL)
	}
}

// TestOwnIdPSkipsTheBroker keeps a self-hosted issuer (ADR-022) talking to its
// own provider: brokering it would route a third party's login through us.
func TestOwnIdPSkipsTheBroker(t *testing.T) {
	t.Setenv("FPCLOUD_OIDC_CLIENT_ID", "some-other-client")
	t.Setenv("FPCLOUD_OIDC_AUTH_URL", "https://idp.example.test/authorize")
	t.Setenv("FPCLOUD_OIDC_TOKEN_URL", "https://idp.example.test/token")
	_ = os.Unsetenv("FPCLOUD_OIDC_ISSUER")

	conf, err := oauthConfig(context.Background(), "http://127.0.0.1:1234")
	if err != nil {
		t.Fatalf("oauthConfig: %v", err)
	}
	if conf.Endpoint.TokenURL != "https://idp.example.test/token" {
		t.Errorf("token url = %q, want the configured issuer's", conf.Endpoint.TokenURL)
	}
}
