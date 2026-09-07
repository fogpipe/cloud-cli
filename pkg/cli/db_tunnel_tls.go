package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// A tunnel URL used to say `sslmode=require`, and `require` was its ceiling:
// the tunnel relayed the client's TLS bytes to the CNPG primary, whose
// certificate names the cluster's Service and chains to a CA the tenant does
// not hold, so a client that verified could never connect
// (fogpipe/cloud-workspace#212, ADR-154). The tunnel now terminates TLS for
// the client itself, with a certificate it mints for the local address and a
// CA it writes beside the CLI's own state, so the URL can say `verify-full`
// and mean it. The API verifies the primary on its side of the tunnel with
// the cluster's CA, which it can read.

// tunnelTLS is the certificate one `db connect` serves on its local port, and
// the file its CA is written to for the printed URL.
type tunnelTLS struct {
	config *tls.Config
	caPath string
}

// mintTunnelTLS creates an ephemeral CA and a leaf certificate for the
// loopback names a client can dial, and writes the CA to a file named after
// the port so two tunnels do not share one.
func mintTunnelTLS(port int) (*tunnelTLS, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	notBefore := time.Now().Add(-5 * time.Minute)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fpcloud db connect"},
		NotBefore:             notBefore,
		NotAfter:              notBefore.Add(48 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:    notBefore,
		NotAfter:     notBefore.Add(48 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}

	dir := stateDir()
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	caPath := filepath.Join(dir, fmt.Sprintf("tunnel-%d-ca.pem", port))
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write tunnel ca: %w", err)
	}

	return &tunnelTLS{
		config: &tls.Config{
			Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}},
			MinVersion:   tls.VersionTLS12,
		},
		caPath: caPath,
	}, nil
}

// remove deletes the CA file; a client keeps a copy of nothing once the
// tunnel is closed.
func (t *tunnelTLS) remove() { _ = os.Remove(t.caPath) }

// Postgres opens with an 8-byte request naming what it wants: TLS, GSS
// encryption, or neither (a StartupMessage, whose first four bytes are its
// length and next four its protocol version).
const (
	pgSSLRequestCode    = 80877103
	pgGSSENCRequestCode = 80877104
)

// terminate answers the client's SSLRequest with a TLS handshake on this
// tunnel's certificate and returns the plaintext session, the way a Postgres
// server would. A client that asks for GSS encryption is told no and asked
// again; one that asks for nothing is returned as it is, with what it already
// sent still ahead of the stream — that client chose not to verify, and the
// `wss` hop still protects the path.
func (t *tunnelTLS) terminate(conn net.Conn) (net.Conn, error) {
	for {
		var head [8]byte
		if _, err := io.ReadFull(conn, head[:]); err != nil {
			return nil, err
		}
		code := uint32(head[4])<<24 | uint32(head[5])<<16 | uint32(head[6])<<8 | uint32(head[7])
		switch code {
		case pgSSLRequestCode:
			if _, err := conn.Write([]byte{'S'}); err != nil {
				return nil, err
			}
			srv := tls.Server(conn, t.config)
			if err := srv.Handshake(); err != nil {
				return nil, fmt.Errorf("tls handshake with the client: %w", err)
			}
			return srv, nil
		case pgGSSENCRequestCode:
			if _, err := conn.Write([]byte{'N'}); err != nil {
				return nil, err
			}
		default:
			return &prefixedConn{Conn: conn, prefix: head[:]}, nil
		}
	}
}

// prefixedConn hands back bytes already read before the rest of the stream.
type prefixedConn struct {
	net.Conn
	prefix []byte
}

func (p *prefixedConn) Read(b []byte) (int, error) {
	if len(p.prefix) > 0 {
		n := copy(b, p.prefix)
		p.prefix = p.prefix[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}
