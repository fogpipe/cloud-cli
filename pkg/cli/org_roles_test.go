package cli

import (
	"testing"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// An org is listed for whichever role reaches it (fogpipe/cloud-workspace#1136),
// so the row says which: the org role and billing role in the API's words, or
// that only project roles reach it, never a blank that reads as no access.
func TestOrgRolesSaysWhatReachesTheOrg(t *testing.T) {
	for _, c := range []struct {
		org  client.Organization
		want string
	}{
		{client.Organization{Role: "owner", BillingRole: "billing.admin"}, "owner, billing.admin"},
		{client.Organization{Role: "viewer"}, "viewer"},
		{client.Organization{BillingRole: "billing.viewer"}, "billing.viewer"},
		{client.Organization{}, "projects only"},
	} {
		if got := orgRoles(&c.org); got != c.want {
			t.Errorf("orgRoles(%+v) = %q, want %q", c.org, got, c.want)
		}
	}
}
