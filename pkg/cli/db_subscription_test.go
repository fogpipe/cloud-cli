package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// subscribeFlags builds the create command's own flag set fresh for each case,
// from the same function the command uses — a shared set would carry one
// case's answers into the next, which is how three of these first passed.
func subscribeFlags(t *testing.T, set map[string]string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "create"}
	registerSubscribeFlags(cmd)
	for name, value := range set {
		require.NoError(t, cmd.Flags().Set(name, value))
	}
	return cmd
}

// The source address is given once, as the URL a tenant already holds, and the
// five fields the API takes are read out of it.
func TestSubscriptionRequestReadsTheSourceFromTheURL(t *testing.T) {
	req, err := subscriptionRequest(subscribeFlags(t, map[string]string{
		"name":        "migrate",
		"publication": "everything",
		"from":        "postgres://tenant:hunter2@db.abcdef.supabase.co:6432/postgres?sslmode=require",
	}))
	require.NoError(t, err)
	assert.Equal(t, "db.abcdef.supabase.co", req.Host)
	assert.Equal(t, int32(6432), req.Port, "the port is the tenant's, not an assumed 5432")
	assert.Equal(t, "tenant", req.User)
	assert.Equal(t, "postgres", req.DBName)
	assert.Equal(t, "require", req.SSLMode)
	assert.Equal(t, "hunter2", req.Password)
}

// A port the tenant did not state is left at zero for the API to default, not
// guessed here: two places defaulting one value is how they come to disagree.
func TestSubscriptionRequestLeavesAnUnstatedPortToTheAPI(t *testing.T) {
	req, err := subscriptionRequest(subscribeFlags(t, map[string]string{
		"name": "migrate", "publication": "everything",
		"from": "postgres://tenant:pw@db.example.com/postgres",
	}))
	require.NoError(t, err)
	assert.Zero(t, req.Port)
	assert.Empty(t, req.SSLMode)
}

// A password nowhere to be found is refused with the three places it can go,
// rather than a subscription declared with an empty credential that fails at
// the source with an authentication error naming nothing.
func TestSubscriptionRequestRefusesAMissingPassword(t *testing.T) {
	t.Setenv("FPCLOUD_SUBSCRIPTION_PASSWORD", "")
	_, err := subscriptionRequest(subscribeFlags(t, map[string]string{
		"name": "migrate", "publication": "everything",
		"from": "postgres://tenant@db.example.com:5432/postgres",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FPCLOUD_SUBSCRIPTION_PASSWORD")
	assert.Contains(t, err.Error(), "--password-stdin")
}

// The environment is the place to put a password that should not be in shell
// history, and it is read when the URL carries none.
func TestSubscriptionRequestTakesThePasswordFromTheEnvironment(t *testing.T) {
	t.Setenv("FPCLOUD_SUBSCRIPTION_PASSWORD", "from-the-env")
	req, err := subscriptionRequest(subscribeFlags(t, map[string]string{
		"name": "migrate", "publication": "everything",
		"from": "postgres://tenant@db.example.com:5432/postgres",
	}))
	require.NoError(t, err)
	assert.Equal(t, "from-the-env", req.Password)
}

func TestSubscriptionRequestRefusesWhatItCannotBuild(t *testing.T) {
	t.Setenv("FPCLOUD_SUBSCRIPTION_PASSWORD", "")
	for _, tc := range []struct {
		name  string
		flags map[string]string
		want  string
	}{
		{"no source", map[string]string{"name": "m", "publication": "p"}, "--from is required"},
		{"not postgres", map[string]string{"name": "m", "publication": "p", "from": "mysql://h/db"}, "must be a postgres:// URL"},
		{"no name", map[string]string{"publication": "p", "from": "postgres://u:p@h:5432/db"}, "--name is required"},
		{"no publication", map[string]string{"name": "m", "from": "postgres://u:p@h:5432/db"}, "--publication is required"},
		{"port is not a number", map[string]string{"name": "m", "publication": "p", "from": "postgres://u:p@h:umm/db"}, "not a connection URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := subscriptionRequest(subscribeFlags(t, tc.flags))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// Parameters are key=value and a bare word is refused, so a typo becomes an
// error here rather than an option the source silently never receives.
func TestSubscriptionRequestParsesParameters(t *testing.T) {
	req, err := subscriptionRequest(subscribeFlags(t, map[string]string{
		"name": "m", "publication": "p", "from": "postgres://u:pw@h:5432/db",
		"parameter": "copy_data=false,slot_name=migrate_slot",
	}))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"copy_data": "false", "slot_name": "migrate_slot"}, req.Parameters)

	_, err = subscriptionRequest(subscribeFlags(t, map[string]string{
		"name": "m", "publication": "p", "from": "postgres://u:pw@h:5432/db",
		"parameter": "copy_data",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key=value")
}

// `applied` is the operand's answer to "did the statement run" and stays true
// on a subscription that has since died (ADR-172). Nothing in the rendering may
// present it as the state, so the state comes from health and only from health.
func TestSubscriptionStateIsReadFromHealthNotFromApplied(t *testing.T) {
	dead := client.DatabaseSubscription{
		Name:    "migrate",
		Applied: true,
		Health: client.DatabaseSubscriptionHealth{
			State:  client.SubscriptionDown,
			Reason: "no apply worker",
		},
	}

	state := renderSubscriptionState(dead.Health)
	assert.Contains(t, state, "down")
	assert.Contains(t, state, "no apply worker")
	assert.NotContains(t, state, "replicating")

	// And the detail view shows `applied` for what it is rather than as the
	// state — a reader who takes it for liveness reads a dead subscription as
	// a healthy one, which is the whole reason health is read from Postgres.
	rows := subscriptionRows(&dead)
	var state1, applied string
	for _, row := range rows {
		switch row[0] {
		case "State":
			state1 = row[1]
		case "Statement applied":
			applied = row[1]
		}
	}
	assert.Contains(t, state1, "down")
	assert.Equal(t, "true", applied, "the operand still says the statement ran, and it is shown saying only that")
}

// An unreadable subscriber is neither healthy nor broken, and says which it is.
func TestSubscriptionUnknownCarriesWhyNobodyCouldAsk(t *testing.T) {
	sub := client.DatabaseSubscription{
		Name:   "migrate",
		Health: client.DatabaseSubscriptionHealth{State: client.SubscriptionUnknown, Unreadable: "connect: connection refused"},
	}
	assert.Contains(t, renderSubscriptionState(sub.Health), "unknown")

	var why string
	for _, row := range subscriptionRows(&sub) {
		if row[0] == "Why unknown" {
			why = row[1]
		}
	}
	assert.Equal(t, "connect: connection refused", why)

	// Lag and error counts are absent rather than zero: a zero here would be a
	// reading nobody took, rendered as the healthiest number available.
	for _, row := range subscriptionRows(&sub) {
		switch row[0] {
		case "Apply lag", "Apply errors", "Sync errors":
			assert.Equal(t, "-", row[1], "%s must not read as zero when nobody could ask", row[0])
		}
	}
}
