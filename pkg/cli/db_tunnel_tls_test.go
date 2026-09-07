package cli

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// A client that verifies connects: the tunnel's certificate names 127.0.0.1
// and chains to the CA the URL points at (fogpipe/cloud-workspace#212).
func TestTunnelTLS_AVerifyingClientConnects(t *testing.T) {
	isolateState(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	tt, err := mintTunnelTLS(port)
	require.NoError(t, err)
	defer tt.remove()

	// The tunnel's side: terminate, then echo the plaintext session.
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		session, err := tt.terminate(conn)
		if err != nil {
			return
		}
		_, _ = io.Copy(session, session)
	}()

	caPEM, err := os.ReadFile(tt.caPath)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(caPEM))

	raw, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer raw.Close()
	// libpq's opening: SSLRequest, expect 'S', then a handshake that verifies
	// the host, which is what verify-full does.
	_, err = raw.Write([]byte{0, 0, 0, 8, 0x04, 0xd2, 0x16, 0x2f})
	require.NoError(t, err)
	var answer [1]byte
	_, err = io.ReadFull(raw, answer[:])
	require.NoError(t, err)
	require.Equal(t, byte('S'), answer[0])
	client := tls.Client(raw, &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12})
	require.NoError(t, client.Handshake(), "verify-full against 127.0.0.1 must pass")

	_, err = client.Write([]byte("select 1"))
	require.NoError(t, err)
	buf := make([]byte, 8)
	_, err = io.ReadFull(client, buf)
	require.NoError(t, err)
	require.Equal(t, "select 1", string(buf))
}

// A client that asked for no TLS is relayed as it is, with its opening bytes
// still ahead of the stream.
func TestTunnelTLS_APlaintextClientIsRelayedIntact(t *testing.T) {
	isolateState(t)
	tt, err := mintTunnelTLS(0)
	require.NoError(t, err)
	defer tt.remove()

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	startup := []byte{0, 0, 0, 12, 0, 3, 0, 0, 'u', 's', 'e', 'r'}
	go func() { _, _ = client.Write(startup) }()
	session, err := tt.terminate(server)
	require.NoError(t, err)
	got := make([]byte, len(startup))
	_, err = io.ReadFull(session, got)
	require.NoError(t, err)
	require.Equal(t, startup, got)
}
