package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

func TestParseProbeSpec(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    *client.ProbeSpec
		wantErr string
	}{
		{name: "empty is no override", value: ""},
		{name: "whitespace is no override", value: "   "},
		{
			// The common case: point one probe somewhere else and inherit every
			// timing field from the app's shared --health-check-* settings.
			name:  "path only",
			value: "path=/healthz",
			want:  &client.ProbeSpec{Path: "/healthz"},
		},
		{
			name:  "every field",
			value: "path=/ready,initial_delay_seconds=5,period_seconds=10,timeout_seconds=2,failure_threshold=3,success_threshold=2",
			want: &client.ProbeSpec{
				Path:                "/ready",
				InitialDelaySeconds: 5,
				PeriodSeconds:       10,
				TimeoutSeconds:      2,
				FailureThreshold:    3,
				SuccessThreshold:    2,
			},
		},
		{
			// A timing-only override is legitimate: keep the shared health path,
			// just give this probe a longer runway.
			name:  "timing without a path",
			value: "failure_threshold=30",
			want:  &client.ProbeSpec{FailureThreshold: 30},
		},
		{name: "spaces around fields", value: " path=/healthz , period_seconds=10 ", want: &client.ProbeSpec{Path: "/healthz", PeriodSeconds: 10}},
		{name: "trailing comma", value: "path=/healthz,", want: &client.ProbeSpec{Path: "/healthz"}},

		{name: "bare word", value: "healthz", wantErr: "want key=value"},
		{name: "missing value", value: "path=", wantErr: "want key=value"},
		{name: "unknown field", value: "interval=10", wantErr: `unknown --liveness field "interval"`},
		{name: "relative path", value: "path=healthz", wantErr: `must start with "/"`},
		// A non-numeric or zero timing would otherwise parse as "unset" and
		// silently fall back to the shared value — the opposite of what was asked.
		{name: "non-numeric timing", value: "period_seconds=fast", wantErr: "positive whole number"},
		{name: "zero timing", value: "period_seconds=0", wantErr: "positive whole number"},
		{name: "negative timing", value: "period_seconds=-1", wantErr: "positive whole number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProbeSpec("liveness", tt.value)
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

func TestProbeOverridesFromFlags(t *testing.T) {
	set := func(t *testing.T, args ...string) *cobra.Command {
		t.Helper()
		cmd := &cobra.Command{Use: "test"}
		registerProbeFlags(cmd)
		require.NoError(t, cmd.ParseFlags(args))
		return cmd
	}

	t.Run("no flags means no overrides", func(t *testing.T) {
		got, err := probeOverridesFromFlags(set(t))
		require.NoError(t, err)
		assert.Nil(t, got, "nil is what puts every probe back on the shared health check")
	})

	t.Run("one probe leaves the others inheriting", func(t *testing.T) {
		got, err := probeOverridesFromFlags(set(t, "--liveness", "path=/healthz"))
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, &client.ProbeSpec{Path: "/healthz"}, got.Liveness)
		assert.Nil(t, got.Readiness)
		assert.Nil(t, got.Startup)
	})

	t.Run("all three", func(t *testing.T) {
		got, err := probeOverridesFromFlags(set(t,
			"--liveness", "path=/healthz",
			"--readiness", "path=/ready,failure_threshold=2",
			"--startup", "failure_threshold=30",
		))
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, &client.ProbeSpec{Path: "/healthz"}, got.Liveness)
		assert.Equal(t, &client.ProbeSpec{Path: "/ready", FailureThreshold: 2}, got.Readiness)
		assert.Equal(t, &client.ProbeSpec{FailureThreshold: 30}, got.Startup)
	})

	t.Run("the flag name reaches the error", func(t *testing.T) {
		_, err := probeOverridesFromFlags(set(t, "--startup", "nope"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--startup")
	})
}

func TestProbeSummaries(t *testing.T) {
	t.Run("no overrides", func(t *testing.T) {
		assert.Nil(t, probeSummaries(&client.App{HealthCheckPath: "/healthz"}))
	})

	t.Run("inherited path is shown, inherited timing is not", func(t *testing.T) {
		// Timing the app didn't override belongs to the Health Check row; repeating
		// it here would report a shared value as a per-probe one.
		got := probeSummaries(&client.App{
			HealthCheckPath: "/healthz",
			Probes: &client.ProbeOverrides{
				Liveness:  &client.ProbeSpec{Path: "/livez"},
				Readiness: &client.ProbeSpec{FailureThreshold: 2},
			},
		})
		assert.Equal(t, []string{"liveness /livez", "readiness /healthz (failures=2)"}, got)
	})

	t.Run("ordered liveness, readiness, startup", func(t *testing.T) {
		got := probeSummaries(&client.App{
			HealthCheckPath: "/healthz",
			Probes: &client.ProbeOverrides{
				Startup:   &client.ProbeSpec{Path: "/boot"},
				Readiness: &client.ProbeSpec{Path: "/ready"},
				Liveness:  &client.ProbeSpec{Path: "/livez"},
			},
		})
		assert.Equal(t, []string{"liveness /livez", "readiness /ready", "startup /boot"}, got)
	})
}
