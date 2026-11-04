package cli

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

func TestProjectAccessOf(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"read", nil, "yes"},
		{"refused", &client.APIError{StatusCode: http.StatusForbidden}, "no"},
		{"missing", &client.APIError{StatusCode: http.StatusNotFound}, "no"},
		{"wrapped refusal", fmt.Errorf("get: %w", &client.APIError{StatusCode: http.StatusForbidden}), "no"},
		{"the server failing says nothing about the project", &client.APIError{StatusCode: http.StatusBadGateway}, "unknown"},
		{"no answer at all says nothing about the project", errors.New("dial tcp: connection refused"), "unknown"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := projectAccessOf(c.err); got != c.want {
				t.Errorf("projectAccessOf = %q, want %q", got, c.want)
			}
		})
	}
}
