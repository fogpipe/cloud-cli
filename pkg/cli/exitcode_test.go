package cli

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

func TestExitCode(t *testing.T) {
	api := func(status int) error {
		return fmt.Errorf("wrapped: %w", &client.APIError{StatusCode: status, Code: "X"})
	}
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, exitOK},
		{"usage", usageError{errors.New("unknown flag: --x")}, exitUsage},
		{"not found", api(404), exitNotFound},
		{"unauthorized", api(401), exitAuth},
		{"forbidden", api(403), exitAuth},
		{"rate limited", api(429), exitUnavailable},
		{"bad gateway", api(502), exitUnavailable},
		{"unavailable", api(503), exitUnavailable},
		{"gateway timeout", api(504), exitUnavailable},
		{"conflict stays general", api(409), exitFailure},
		{"validation stays general", api(400), exitFailure},
		{"server error stays general", api(500), exitFailure},
		{"transport", fmt.Errorf("executing request: %w", &url.Error{Op: "Get", URL: "https://x", Err: errors.New("connection refused")}), exitUnavailable},
		{"plain error", errors.New("something"), exitFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, exitCode(tc.err))
		})
	}
}

func TestUnknownSubcommand(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"project", "show", "x"}, "show"},
		{[]string{"--api-url", "http://x", "project", "show", "x"}, "show"},
		{[]string{"project", "-o", "json", "show"}, "show"},
		{[]string{"project", "--help-llm", "show"}, "show"},
		{[]string{"project"}, ""},
		{[]string{"project", "--help"}, ""},
		{[]string{"project", "list", "x"}, ""},
		{[]string{"nosuchcmd"}, ""},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			_, word, ok := unknownSubcommand(tc.args)
			assert.Equal(t, tc.want != "", ok)
			assert.Equal(t, tc.want, word)
		})
	}
}
