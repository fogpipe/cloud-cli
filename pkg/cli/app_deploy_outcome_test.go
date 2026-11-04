package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// A deploy the cluster refused is recorded as a failed deployment and answered
// with the app marked failed, on a 200. The CLI used to read that record only
// behind a release command, so an app without one rendered a success box above
// "Status failed" and exited zero; a release pipeline built on that exit code
// went on to the next step. Whatever the output format, a failed record is an
// error carrying the API's own message.
func TestDeployOutcome_FailedRecordIsAnErrorWithTheMessage(t *testing.T) {
	c, _ := fakeDeployAPI(t, []*client.Deployment{{
		ID: "dep-1", AppID: "app-1", Status: "failed",
		Message: "observe domains for web: certificate for web.example.com: list challenges in fp-acme: forbidden",
	}})

	for _, format := range []string{"table", "json"} {
		_, err := deployOutcome(waitFlag(false), c, "app-1", &client.App{ID: "app-1", Status: "failed"}, format)
		require.Error(t, err, format)
		assert.Contains(t, err.Error(), "list challenges in fp-acme: forbidden", format)
	}
}

// A failed record with nothing written to it, and an app marked failed with
// no record at all, are both still failures: the message is the API's to
// supply, and its absence does not make the deploy a success.
func TestDeployOutcome_FailureWithoutAMessageIsStillAnError(t *testing.T) {
	c, _ := fakeDeployAPI(t, []*client.Deployment{{ID: "dep-1", AppID: "app-1", Status: "failed"}})
	_, err := deployOutcome(waitFlag(false), c, "app-1", &client.App{ID: "app-1", Status: "failed"}, "json")
	assert.ErrorContains(t, err, "fpcloud app deployments")

	c, _ = fakeDeployAPI(t, nil)
	_, err = deployOutcome(waitFlag(false), c, "app-1", &client.App{ID: "app-1", Status: "failed"}, "json")
	assert.ErrorContains(t, err, "fpcloud app deployments")
}

// A settled record that is not failed is the ordinary outcome, and the app is
// read back fresh so the output carries the status the rollout reached rather
// than the one the call returned.
func TestDeployOutcome_ActiveRecordReturnsTheFreshApp(t *testing.T) {
	c, reads := fakeDeployAPI(t, []*client.Deployment{{ID: "dep-1", AppID: "app-1", Status: "active"}})
	app, err := deployOutcome(waitFlag(false), c, "app-1", &client.App{ID: "app-1", Status: "deploying"}, "json")
	require.NoError(t, err)
	assert.Equal(t, "running", app.Status)
	assert.Equal(t, 1, *reads)
}

func waitFlag(wait bool) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("wait", wait, "")
	return cmd
}

// A deploy is accepted long before its rollout ends. A command returns once the
// record says the rollout has started, saying it continues; a rollout that later
// fails is on the record.
func TestDeployOutcome_ARolloutIsNotWaitedOnUnlessAsked(t *testing.T) {
	c, _ := fakeDeployAPI(t, []*client.Deployment{{ID: "dep-1", AppID: "app-1", Status: "deploying"}})
	app, err := deployOutcome(waitFlag(false), c, "app-1", &client.App{ID: "app-1", Status: "deploying"}, "json")
	require.NoError(t, err)
	assert.Equal(t, "app-1", app.ID)
}

func TestDeploymentSettled(t *testing.T) {
	for _, c := range []struct {
		status     string
		wait, want bool
	}{
		{"releasing", false, false},
		{"releasing", true, false},
		{"deploying", false, true},
		{"deploying", true, false},
		{"active", false, true},
		{"active", true, true},
		{"failed", false, true},
		{"failed", true, true},
		{"superseded", true, true},
	} {
		assert.Equal(t, c.want, deploymentSettled(&client.Deployment{Status: c.status}, c.wait), "%s wait=%v", c.status, c.wait)
	}
}

// Asked to wait, a command follows the record to its verdict and carries it: the
// reason a rollout failed is the API's own, and the exit code is CI's to read.
func TestDeployOutcome_WaitingFollowsTheRolloutToItsVerdict(t *testing.T) {
	useFastPolling(t)
	failing := []*client.Deployment{{ID: "dep-1", AppID: "app-1", Status: "failed", Message: "timed out progressing"}}
	c, _ := fakeDeploySequence(t, []*client.Deployment{{ID: "dep-1", AppID: "app-1", Status: "deploying"}}, failing)
	_, err := deployOutcome(waitFlag(true), c, "app-1", &client.App{ID: "app-1", Status: "deploying"}, "json")
	assert.ErrorContains(t, err, "timed out progressing")

	active := []*client.Deployment{{ID: "dep-1", AppID: "app-1", Status: "active"}}
	c, _ = fakeDeploySequence(t, []*client.Deployment{{ID: "dep-1", AppID: "app-1", Status: "deploying"}}, active)
	app, err := deployOutcome(waitFlag(true), c, "app-1", &client.App{ID: "app-1", Status: "deploying"}, "json")
	require.NoError(t, err)
	assert.Equal(t, "running", app.Status)
}

// A release command is followed whether or not the rollout after it is: a failed
// migration is this command's to report, and it stops following once the gate
// passes and the rollout has started.
func TestDeployOutcome_AReleaseCommandIsAlwaysFollowed(t *testing.T) {
	useFastPolling(t)
	c, _ := fakeDeploySequence(t,
		[]*client.Deployment{{ID: "dep-1", AppID: "app-1", Status: "releasing"}},
		[]*client.Deployment{{ID: "dep-1", AppID: "app-1", Status: "failed", Message: "migration failed"}},
	)
	_, err := deployOutcome(waitFlag(false), c, "app-1", &client.App{ID: "app-1", Status: "deploying"}, "json")
	assert.ErrorContains(t, err, "migration failed")

	c, lists := fakeDeploySequence(t,
		[]*client.Deployment{{ID: "dep-1", AppID: "app-1", Status: "releasing"}},
		[]*client.Deployment{{ID: "dep-1", AppID: "app-1", Status: "deploying"}},
		[]*client.Deployment{{ID: "dep-1", AppID: "app-1", Status: "failed", Message: "must not be read"}},
	)
	_, err = deployOutcome(waitFlag(false), c, "app-1", &client.App{ID: "app-1", Status: "deploying"}, "json")
	require.NoError(t, err)
	assert.Equal(t, 2, *lists, "it stopped at the rollout")
}

// useFastPolling keeps the poll from costing the test real seconds.
func useFastPolling(t *testing.T) {
	t.Helper()
	prev := deploymentPoll
	deploymentPoll = time.Millisecond
	t.Cleanup(func() { deploymentPoll = prev })
}

// fakeDeploySequence serves each list of records in turn, the last one for good,
// and counts the reads.
func fakeDeploySequence(t *testing.T, sequence ...[]*client.Deployment) (*client.Client, *int) {
	t.Helper()
	lists := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/app-1/deployments":
			at := min(lists, len(sequence)-1)
			lists++
			_ = json.NewEncoder(w).Encode(sequence[at])
		case "/api/v1/apps/app-1":
			_ = json.NewEncoder(w).Encode(client.App{ID: "app-1", Name: "web", Status: "running"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return client.New(srv.URL, "fp-test"), &lists
}

// fakeDeployAPI serves one app's deployment list and the app itself, which is
// "running" on every read, and counts how often the app was read.
func fakeDeployAPI(t *testing.T, deployments []*client.Deployment) (*client.Client, *int) {
	t.Helper()
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/app-1/deployments":
			if deployments == nil {
				deployments = []*client.Deployment{}
			}
			_ = json.NewEncoder(w).Encode(deployments)
		case "/api/v1/apps/app-1":
			reads++
			_ = json.NewEncoder(w).Encode(client.App{ID: "app-1", Name: "web", Status: "running"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return client.New(srv.URL, "fp-test"), &reads
}
