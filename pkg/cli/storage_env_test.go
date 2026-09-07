package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A minted key evals into a self-consistent set: this key's id and secret,
// the bucket's endpoint, region and S3 name, in the spelling the console's
// "Copy as env" and get-credentials use (fogpipe/cloud-workspace#331).
func TestS3Env_AMintedKeyIsOneEvalableSet(t *testing.T) {
	out := s3Env("GK1", "s3cret", "https://s3.cloud.fogpipe.com", "garage", "media-proj-k3f")

	require.Equal(t, []string{
		"export AWS_ACCESS_KEY_ID=GK1",
		"export AWS_SECRET_ACCESS_KEY=s3cret",
		"export AWS_ENDPOINT_URL_S3=https://s3.cloud.fogpipe.com",
		"export AWS_REGION=garage",
		"export AWS_S3_BUCKET=media-proj-k3f",
	}, strings.Split(strings.TrimSpace(out), "\n"))
}

// get-credentials has no secret and prints none: an empty export would shadow
// the secret the shell already holds.
func TestS3Env_NoSecretIsNoLine(t *testing.T) {
	out := s3Env("GK1", "", "https://s3.cloud.fogpipe.com", "garage", "media-proj-k3f")

	require.NotContains(t, out, "AWS_SECRET_ACCESS_KEY")
	require.Contains(t, out, "export AWS_ACCESS_KEY_ID=GK1\n")
}
