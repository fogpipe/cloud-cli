package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// The project-wide list does not care what backs a host: an app's custom
// domain, a website's, and the platform host an app was given sit in one
// table, each naming what serves it (fogpipe/cloud-workspace#14).
func TestDomainStatusRows_OneTableWhateverBacksTheHost(t *testing.T) {
	rows := domainStatusRows([]client.DomainStatus{
		{Domain: "shop.example.com", Source: "custom", Mode: "verified", Status: "active", TLSStatus: "issued", OwnerKind: "app", Owner: "web"},
		{Domain: "docs.example.com", Source: "custom", Mode: "verified", Status: "pending", TLSStatus: "pending", OwnerKind: "bucket", Owner: "docs"},
		{Domain: "web-k3f.cloud.fogpipe.com", Source: "platform", Status: "active", TLSStatus: "issued", OwnerKind: "app", Owner: "web"},
	})

	require.Len(t, rows, 3)
	require.Equal(t, "app/web", rows[0][4])
	require.Equal(t, "bucket/docs", rows[1][4])
	require.Equal(t, "verified", rows[0][1])
	require.Contains(t, rows[2][1], "platform", "a platform host has no mode and shows its source instead")
	require.Equal(t, "app/web", rows[2][4])
}
