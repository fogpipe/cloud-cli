package cli

import (
	"testing"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// The audit table names a resource while it exists and falls back to its id
// when the server has no name for it, so a deleted app is still identifiable.
func TestAuditResourceNamesWhatItCan(t *testing.T) {
	named := &client.AuditEntry{ResourceType: "app", ResourceID: "3aba24d9-5bb3", ResourceName: "api"}
	if got := auditResource(named); got != "app:api" {
		t.Fatalf("named resource: got %q, want app:api", got)
	}
	gone := &client.AuditEntry{ResourceType: "app", ResourceID: "3aba24d9-5bb3"}
	if got := auditResource(gone); got != "app:3aba24d9-5bb3" {
		t.Fatalf("unnamed resource: got %q, want the id", got)
	}
}
