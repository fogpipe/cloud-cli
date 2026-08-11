package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

func TestParseRoutes(t *testing.T) {
	tests := []struct {
		name    string
		flags   []string
		want    []client.Route
		wantErr string
	}{
		{name: "none", flags: nil},
		{
			// Carving a path out is the only reason to name one, so the bare form
			// is the common case.
			name:  "bare path defaults to internal",
			flags: []string{"/internal/"},
			want:  []client.Route{{Path: "/internal/", Visibility: "internal"}},
		},
		{
			name:  "explicit visibility",
			flags: []string{"/internal/:internal", "/api/:public"},
			want: []client.Route{
				{Path: "/internal/", Visibility: "internal"},
				{Path: "/api/", Visibility: "public"},
			},
		},
		{
			name:  "repeatable",
			flags: []string{"/internal/", "/admin/"},
			want: []client.Route{
				{Path: "/internal/", Visibility: "internal"},
				{Path: "/admin/", Visibility: "internal"},
			},
		},
		{name: "empty path", flags: []string{":internal"}, wantErr: "invalid --route"},
		{name: "unknown visibility", flags: []string{"/internal/:private"}, wantErr: "invalid --route visibility"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRoutes(tt.flags)
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
