package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// captureStdout runs fn with stdout redirected to a pipe and returns what it
// wrote. Used to prove nothing decorative lands on the data stream.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

// The spinner used to write to stdout, so piping any spinner-using command into
// jq failed on the spinner frame itself ("Invalid numeric literal at line 1,
// column 5"). Progress is decoration; stdout belongs to the data.
func TestWithSpinner_WritesNothingToStdout(t *testing.T) {
	out := captureStdout(t, func() {
		withSpinner("Fetching secret...", func() {})
	})
	assert.Empty(t, out, "spinner output must not reach stdout")
}

// A revealed bundle has to survive a machine pipeline. The human block prints
// KEY=VALUE per line, which silently truncates any multi-line value — a PEM or
// an APNs .p8 key cannot be recovered from it.
func TestOrgSecret_JSONRoundTripsMultilineValues(t *testing.T) {
	const pem = "-----BEGIN PRIVATE KEY-----\nline2\nline3\n-----END PRIVATE KEY-----"
	s := &client.OrgSecret{
		Name: "argus-backend",
		Data: map[string]string{"APNS_KEY": pem, "PLAIN": "v"},
	}

	encoded, err := json.Marshal(s)
	require.NoError(t, err)

	var back client.OrgSecret
	require.NoError(t, json.Unmarshal(encoded, &back))
	assert.Equal(t, pem, back.Data["APNS_KEY"],
		"a multi-line value must round-trip intact through the structured output")
}
