package cli

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/charmbracelet/lipgloss"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

var dbConnectCmd = &cobra.Command{
	Use:     "connect <name>",
	Aliases: []string{"proxy"},
	Short:   "Open a temporary local tunnel to a database and print its connection URL",
	Long: "Opens a tunnel to the database's Postgres primary (databases are\n" +
		"cluster-internal, never internet-exposed) and prints a postgres:// URL on\n" +
		"127.0.0.1 with the live credentials, ready for psql, pg_dump, or any client.\n" +
		"The tunnel rides the API server itself (ADR-045) — no kubectl/FKE access is\n" +
		"needed, only the same database permission `db connection` already requires.\n" +
		"Stays open until Ctrl-C; each local connection (e.g. `pg_dump -j N`'s N\n" +
		"parallel workers) gets its own independent tunnel.\n\n" +
		"The URL says sslmode=verify-full and means it: the tunnel serves a\n" +
		"certificate minted for 127.0.0.1 and names its CA in the URL, so a client\n" +
		"that insists on verifying connects unmodified. The CA file lives for the\n" +
		"tunnel and is removed with it.\n\n" +
		"It also says channel_binding=disable, and means that too: the tunnel is\n" +
		"two TLS sessions (yours to 127.0.0.1, the platform's to the primary), and\n" +
		"SCRAM channel binding ties a login to one session's certificate, so it\n" +
		"cannot hold across both. What verify-full protects is the hop you can see.",
	Args: cobra.ExactArgs(1),
	RunE: runDBConnect,
}

func runDBConnect(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	localPort, _ := cmd.Flags().GetInt("port")

	c := getClient()
	id, err := resolveDatabaseID(c, args[0])
	if err != nil {
		return err
	}
	conn, err := c.GetDatabaseConnection(ctx, id)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		return fmt.Errorf("open local listener: %w", err)
	}
	defer ln.Close()
	local := ln.Addr().(*net.TCPAddr).Port

	// The tunnel terminates TLS for the client with a certificate it mints
	// for 127.0.0.1, so the URL can ask for verification and get it
	// (ADR-154). The API verifies the primary on its side.
	tunnelCert, err := mintTunnelTLS(local)
	if err != nil {
		return fmt.Errorf("mint tunnel certificate: %w", err)
	}
	defer tunnelCert.remove()

	localURL := tunnelURL(conn, local, tunnelCert.caPath)

	if isStructured(rootCmd.Flag("output").Value.String()) {
		if err := renderData(map[string]any{
			"url":         localURL.String(),
			"host":        "127.0.0.1",
			"port":        local,
			"database":    conn.Database,
			"username":    conn.Username,
			"password":    conn.Password,
			"sslrootcert": tunnelCert.caPath,
		}); err != nil {
			return err
		}
	} else {
		fmt.Println(renderInfoBox("Database Tunnel", [][]string{
			{"Database", args[0]},
			{"Local port", fmt.Sprintf("%d", local)},
			{"CA", tunnelCert.caPath},
			{"URL", lipgloss.NewStyle().Bold(true).Foreground(colorInfo).Render(localURL.String())},
		}))
		fmt.Println(mutedStyle.Render("Tunnel open — press Ctrl-C to close."))
	}

	var wg sync.WaitGroup
	acceptErrCh := make(chan error, 1)
	go func() {
		for {
			localConn, err := ln.Accept()
			if err != nil {
				acceptErrCh <- err
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				handleTunnelConn(ctx, c, id, tunnelCert, localConn)
			}()
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-acceptErrCh:
		return fmt.Errorf("local listener: %w", err)
	case <-sig:
		ln.Close()
	}
	wg.Wait()
	return nil
}

// handleTunnelConn relays one local connection (one psql session, one
// pg_dump worker) over its own db-connect tunnel (ADR-045).
func handleTunnelConn(ctx context.Context, c *client.Client, databaseID string, tunnelCert *tunnelTLS, local net.Conn) {
	defer local.Close()
	session, err := tunnelCert.terminate(local)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tunnel: %v\n", err)
		return
	}
	local = session
	ws, err := c.DialTunnel(ctx, databaseID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tunnel: %v\n", err)
		return
	}
	defer ws.Close()
	relayLocal(local, ws)
}

// relayLocal copies bytes bidirectionally between a local TCP connection and
// the db-connect WebSocket tunnel until either side closes or errors. Only
// this goroutine plus the one it spawns ever write to ws, so no write mutex
// is needed here (unlike the server side, which also has a ping ticker).
// Server-sent pings are answered automatically by gorilla's default handler
// as a side effect of this loop calling ReadMessage.
func relayLocal(local net.Conn, ws *websocket.Conn) {
	done := make(chan struct{})
	var once sync.Once
	closeAll := func() {
		once.Do(func() {
			close(done)
			local.Close()
			ws.Close()
		})
	}
	defer closeAll()

	go func() {
		defer closeAll()
		buf := make([]byte, 32*1024)
		for {
			n, err := local.Read(buf)
			if n > 0 {
				if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		mt, msg, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if mt != websocket.BinaryMessage {
			continue
		}
		if _, werr := local.Write(msg); werr != nil {
			return
		}
	}
}

func init() {
	dbConnectCmd.Flags().Int("port", 0, "Local port to listen on (default: a random free port)")
	dbCmd.AddCommand(dbConnectCmd)
}

// tunnelURL is the connection the tunnel offers, stated exactly.
//
// verify-full names the local hop's certificate and CA (ADR-154).
// channel_binding=disable names what that design cannot give: the primary
// offers SCRAM-SHA-256-PLUS because the platform's hop to it is TLS, libpq
// defaults to prefer and so binds the login to the LOCAL session's
// certificate, and the server checks it against its own — two certificates,
// and every modern psql was refused with "SCRAM channel binding check failed"
// (fogpipe/cloud-workspace#770). Channel binding cannot cross a relay that
// re-terminates TLS, and the URL says so rather than leaving it to the client
// to find out.
func tunnelURL(conn *client.DatabaseConnection, port int, caPath string) url.URL {
	return url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(conn.Username, conn.Password),
		Host:     fmt.Sprintf("127.0.0.1:%d", port),
		Path:     "/" + conn.Database,
		RawQuery: "sslmode=verify-full&sslrootcert=" + url.QueryEscape(caPath) + "&channel_binding=disable",
	}
}
