package client

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAQueryRoundTripsThroughTheCodec(t *testing.T) {
	sent := ExecQuery{Cmd: []string{"sh", "-c", "echo hi"}, Container: "web", TTY: true}
	encoded := encodeQuery(sent)
	values, err := url.ParseQuery(encoded[1:])
	require.NoError(t, err)

	var got ExecQuery
	require.NoError(t, DecodeQuery(values, &got))
	assert.Equal(t, sent, got)
}

func TestAZeroQueryEncodesToNothing(t *testing.T) {
	assert.Equal(t, "", encodeQuery(AuditQuery{}))
}

func TestAParameterThatDoesNotParseIsNamed(t *testing.T) {
	var q AuditQuery
	assert.EqualError(t, DecodeQuery(url.Values{"limit": {"ten"}}, &q), `limit: "ten" is not a whole number`)

	var c DatabaseConnectionQuery
	assert.EqualError(t, DecodeQuery(url.Values{"read_only": {"yes"}}, &c), `read_only: "yes" is not true or false`)
}

func TestAnEmptyOptionalParameterIsStillSent(t *testing.T) {
	empty := ""
	encoded := encodeQuery(BucketLifecycleRuleQuery{Prefix: &empty})
	assert.Equal(t, "?prefix=", encoded)

	var got BucketLifecycleRuleQuery
	require.NoError(t, DecodeQuery(url.Values{"prefix": {""}}, &got))
	require.NotNil(t, got.Prefix)
	assert.Equal(t, "", *got.Prefix)
}
