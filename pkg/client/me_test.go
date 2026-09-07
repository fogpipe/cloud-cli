package client

import "testing"

// A service account is a principal the platform issues, so `me` answers for it
// (fogpipe/cloud-workspace#802). The helpers exist so no caller branches on
// Type by hand — the CLI printed `me.User.Name` unguarded, which is a nil
// dereference for every service account, and that is the shape this prevents.
func TestMeResponseAnswersForEitherPrincipal(t *testing.T) {
	user := &MeResponse{Type: "user", User: &User{Name: "Ada", Email: "ada@example.com"}}
	if got := user.Email(); got != "ada@example.com" {
		t.Errorf("user Email() = %q", got)
	}
	if got := user.DisplayName(); got != "Ada" {
		t.Errorf("user DisplayName() = %q", got)
	}

	sa := &MeResponse{Type: "serviceAccount", ServiceAccount: &ServiceAccount{DisplayName: "Deployer", Email: "deployer@p.o.example.com"}}
	if got := sa.Email(); got != "deployer@p.o.example.com" {
		t.Errorf("service account Email() = %q — a machine identity has an address and blanking it reads as not logged in", got)
	}
	if got := sa.DisplayName(); got != "Deployer" {
		t.Errorf("service account DisplayName() = %q", got)
	}

	// No display name: fall back to the address rather than to empty, so a
	// caller printing this never renders a blank where an identity belongs.
	bare := &MeResponse{Type: "serviceAccount", ServiceAccount: &ServiceAccount{Email: "ci@p.o.example.com"}}
	if got := bare.DisplayName(); got != "ci@p.o.example.com" {
		t.Errorf("bare DisplayName() = %q, want the address", got)
	}

	// Neither populated, and a nil receiver: both are answers a caller can
	// print, not panics. The old code panicked on exactly this shape.
	if got := (&MeResponse{Type: "user"}).Email(); got != "" {
		t.Errorf("empty Email() = %q", got)
	}
	var nilMe *MeResponse
	if got := nilMe.DisplayName(); got != "" {
		t.Errorf("nil DisplayName() = %q", got)
	}
}
