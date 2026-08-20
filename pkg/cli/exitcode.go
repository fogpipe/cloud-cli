package cli

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// Exit statuses. A caller branches on these, never on stderr — prose is not a
// contract. The classification is the API's own: the status and code every
// failure already arrives with, mapped onto a small fixed set, so the CLI never
// grows a second vocabulary that drifts from the server's. The one property the
// set must keep whatever the numbers: transient (exitUnavailable) is told apart
// from permanent, because that is the branch every caller takes — retry, or
// give up.
const (
	exitOK          = 0
	exitFailure     = 1 // anything not classified below; never also a specific class
	exitUsage       = 2 // the command line itself: unknown command, bad flag — no request was made
	exitNotFound    = 3 // the named resource does not exist (404)
	exitAuth        = 4 // not authenticated, or not allowed (401, 403)
	exitUnavailable = 5 // could not be served right now: transport failure, 429, 502–504 — worth retrying
)

// notFoundError marks a name the CLI resolved itself — through a list, before
// any request could 404 — and did not find. Typed, so the exit status is the
// same as when the API says it, without anyone matching on the message.
type notFoundError struct{ error }

func notFoundf(format string, args ...any) error {
	return notFoundError{fmt.Errorf(format, args...)}
}

// exitCode classifies an error from a command into an exit status.
func exitCode(err error) int {
	if err == nil {
		return exitOK
	}
	if isUsageError(err) {
		return exitUsage
	}
	var nf notFoundError
	if errors.As(err, &nf) {
		return exitNotFound
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusNotFound:
			return exitNotFound
		case http.StatusUnauthorized, http.StatusForbidden:
			return exitAuth
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return exitUnavailable
		}
		return exitFailure
	}
	// A request that never got an answer: refused, reset, timed out, unresolvable.
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return exitUnavailable
	}
	return exitFailure
}

// exitCodeHelp documents the statuses where agents read.
const exitCodeHelp = `EXIT CODES (branch on these, not on stderr):
  0  success
  1  failure not classified below
  2  usage: unknown command or flag — no request was made; do not retry
  3  not found: the named resource does not exist; do not retry
  4  not authenticated or not allowed (401/403); fix the credential, do not retry
  5  unavailable: the control plane could not be reached or answered 429/502/503/504; retry with backoff
Notes: a project-level name you cannot see exits 4, not 3 — the API answers 403
rather than disclose whether it exists. A bare command group (fpcloud project)
prints help and exits 0; an unrecognised word under one (fpcloud project show)
is a usage error, exit 2.
`
