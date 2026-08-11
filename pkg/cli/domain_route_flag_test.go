package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// A UUID backend resolves without a server, so the flag parsing is exercised on
// its own.
const (
	apiAppID = "6f1c2a10-0000-4000-8000-000000000001"
	v2AppID  = "6f1c2a10-0000-4000-8000-000000000002"
)

func TestParseDomainRoutes(t *testing.T) {
	tests := []struct {
		name    string
		flags   []string
		want    []client.DomainRoute
		wantErr string
	}{
		{name: "none", flags: nil, want: []client.DomainRoute{}},
		{
			name:  "path=app",
			flags: []string{"/api/=" + apiAppID},
			want:  []client.DomainRoute{{Path: "/api/", AppID: apiAppID}},
		},
		{
			name:  "repeatable",
			flags: []string{"/api/=" + apiAppID, "/api/v2/=" + v2AppID},
			want: []client.DomainRoute{
				{Path: "/api/", AppID: apiAppID},
				{Path: "/api/v2/", AppID: v2AppID},
			},
		},
		{
			name:  "surrounding whitespace is trimmed",
			flags: []string{" /api/ = " + apiAppID + " "},
			want:  []client.DomainRoute{{Path: "/api/", AppID: apiAppID}},
		},
		{
			// Without the backend the rule says nothing; guessing one would route
			// traffic somewhere the caller never named.
			name:    "path with no app",
			flags:   []string{"/api/"},
			wantErr: "expected path=app",
		},
		{name: "empty app", flags: []string{"/api/="}, wantErr: "expected path=app"},
		{name: "empty path", flags: []string{"=" + apiAppID}, wantErr: "expected path=app"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDomainRoutes(nil, tt.flags)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
