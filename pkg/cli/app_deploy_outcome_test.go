package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
		_, err := deployOutcome(c, "app-1", &client.App{ID: "app-1", Status: "failed"}, format)
		require.Error(t, err, format)
		assert.Contains(t, err.Error(), "list challenges in fp-acme: forbidden", format)
	}
}

// A failed record with nothing written to it, and an app marked failed with
// no record at all, are both still failures: the message is the API's to
// supply, and its absence does not make the deploy a success.
func TestDeployOutcome_FailureWithoutAMessageIsStillAnError(t *testing.T) {
	c, _ := fakeDeployAPI(t, []*client.Deployment{{ID: "dep-1", AppID: "app-1", Status: "failed"}})
	_, err := deployOutcome(c, "app-1", &client.App{ID: "app-1", Status: "failed"}, "json")
	assert.ErrorContains(t, err, "fpcloud app deployments")

	c, _ = fakeDeployAPI(t, nil)
	_, err = deployOutcome(c, "app-1", &client.App{ID: "app-1", Status: "failed"}, "json")
	assert.ErrorContains(t, err, "fpcloud app deployments")
}

// A settled record that is not failed is the ordinary outcome, and the app is
// read back fresh so the output carries the status the rollout reached rather
// than the one the call returned.
func TestDeployOutcome_ActiveRecordReturnsTheFreshApp(t *testing.T) {
	c, reads := fakeDeployAPI(t, []*client.Deployment{{ID: "dep-1", AppID: "app-1", Status: "active"}})
	app, err := deployOutcome(c, "app-1", &client.App{ID: "app-1", Status: "deploying"}, "json")
	require.NoError(t, err)
	assert.Equal(t, "running", app.Status)
	assert.Equal(t, 1, *reads)
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
