package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

var dbSubscriptionCmd = &cobra.Command{
	Use:     "subscription",
	Aliases: []string{"subscriptions", "sub"},
	Short:   "Replicate into a managed database from an external Postgres",
	Long: `Subscribe a managed database to a publication on another Postgres, so
moving onto fpcloud costs a connection switch instead of a full dump and
restore.

Schema is not replicated. Load the schema into the managed database first
(pg_dump --schema-only | psql through ` + "`fpcloud db connect`" + `), then
subscribe for the data — a subscription against an empty database connects and
moves nothing, which looks like a broken feature and is a missing step.`,
}

var dbSubscribeCmd = &cobra.Command{
	Use:   "create <database>",
	Short: "Subscribe a database to a publication on an external source",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveDatabaseID(c, args[0])
		if err != nil {
			return err
		}
		req, err := subscriptionRequest(cmd)
		if err != nil {
			return err
		}
		sub, err := c.CreateDatabaseSubscription(context.Background(), id, *req)
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(sub)
		}
		fmt.Println(renderInfoBox("Subscription "+sub.Name, subscriptionRows(sub)))
		fmt.Println()
		fmt.Println(mutedStyle.Render("  The initial copy runs in the background. Watch it with: fpcloud db subscription show " + args[0] + " " + sub.Name))
		return nil
	},
}

var dbSubscriptionListCmd = &cobra.Command{
	Use:     "list <database>",
	Aliases: []string{"ls"},
	Short:   "List a database's subscriptions and what they are doing",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveDatabaseID(c, args[0])
		if err != nil {
			return err
		}
		subs, err := c.ListDatabaseSubscriptions(context.Background(), id)
		if err != nil {
			return err
		}
		rows := make([][]string, len(subs))
		for i, sub := range subs {
			rows[i] = []string{
				sub.Name,
				sub.Publication,
				fmt.Sprintf("%s:%d/%s", sub.Source.Host, sub.Source.Port, sub.Source.DBName),
				renderSubscriptionState(sub.Health),
				subscriptionLag(sub.Health),
			}
		}
		render([]string{"NAME", "PUBLICATION", "SOURCE", "STATE", "LAG"}, rows, subs)
		return nil
	},
}

var dbSubscriptionShowCmd = &cobra.Command{
	Use:   "show <database> <name>",
	Short: "Show one subscription and the health its subscriber reports",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveDatabaseID(c, args[0])
		if err != nil {
			return err
		}
		sub, err := c.GetDatabaseSubscription(context.Background(), id, args[1])
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(sub)
		}
		fmt.Println(renderInfoBox("Subscription "+sub.Name, subscriptionRows(sub)))
		return nil
	},
}

var dbUnsubscribeCmd = &cobra.Command{
	Use:     "delete <database> <name>",
	Aliases: []string{"rm"},
	Short:   "Drop a subscription",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if yes, _ := cmd.Flags().GetBool("yes"); !yes {
			ok, err := confirm("Drop subscription "+args[1]+"?",
				"Replication stops. Data already applied stays; the replication slot on the source is yours to drop if this cannot reach it.",
				"Drop it")
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		c := getClient()
		id, err := resolveDatabaseID(c, args[0])
		if err != nil {
			return err
		}
		if err := c.DeleteDatabaseSubscription(context.Background(), id, args[1]); err != nil {
			return err
		}
		fmt.Println("Subscription " + args[1] + " dropped.")
		fmt.Println(mutedStyle.Render("  If the source was unreachable, its replication slot is still there and still retaining WAL."))
		return nil
	},
}

// subscriptionRequest turns the flags into the request body, resolving the
// source address from a connection URL so a tenant pastes what they already
// have rather than restating it five times.
func subscriptionRequest(cmd *cobra.Command) (*client.CreateDatabaseSubscriptionRequest, error) {
	from := flagStr(cmd, "from")
	if from == "" {
		return nil, errors.New("--from is required: the source connection URL, e.g. postgres://user@db.example.com:5432/postgres")
	}
	u, err := url.Parse(from)
	if err != nil {
		return nil, fmt.Errorf("--from is not a connection URL: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, fmt.Errorf("--from must be a postgres:// URL, got %q", u.Scheme)
	}
	port := int32(0)
	if p := u.Port(); p != "" {
		n, cerr := strconv.Atoi(p)
		if cerr != nil {
			return nil, fmt.Errorf("--from has a port that is not a number: %q", p)
		}
		port = int32(n)
	}
	req := &client.CreateDatabaseSubscriptionRequest{
		Name:              flagStr(cmd, "name"),
		Publication:       flagStr(cmd, "publication"),
		PublicationDBName: flagStr(cmd, "publication-dbname"),
		Host:              u.Hostname(),
		Port:              port,
		User:              u.User.Username(),
		DBName:            strings.TrimPrefix(u.Path, "/"),
		SSLMode:           u.Query().Get("sslmode"),
	}
	if req.Name == "" {
		return nil, errors.New("--name is required")
	}
	if req.Publication == "" {
		return nil, errors.New("--publication is required: the name of the publication on the source")
	}
	password, err := subscriptionPassword(cmd, u)
	if err != nil {
		return nil, err
	}
	req.Password = password

	params, _ := cmd.Flags().GetStringSlice("parameter")
	if len(params) > 0 {
		req.Parameters = map[string]string{}
		for _, kv := range params {
			key, value, found := strings.Cut(kv, "=")
			if !found {
				return nil, fmt.Errorf("--parameter takes key=value, got %q", kv)
			}
			req.Parameters[key] = value
		}
	}
	return req, nil
}

// subscriptionPassword takes the source password from wherever it was given.
// A URL is the convenient place and the worst one — it is in shell history and
// in the process table — so it is accepted and the alternatives are named.
func subscriptionPassword(cmd *cobra.Command, u *url.URL) (string, error) {
	if stdin, _ := cmd.Flags().GetBool("password-stdin"); stdin {
		raw, err := readAllStdin()
		if err != nil {
			return "", err
		}
		return strings.TrimRight(raw, "\r\n"), nil
	}
	if p, ok := u.User.Password(); ok && p != "" {
		return p, nil
	}
	if p := os.Getenv("FPCLOUD_SUBSCRIPTION_PASSWORD"); p != "" {
		return p, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("no source password: put it in --from, set FPCLOUD_SUBSCRIPTION_PASSWORD, or pass --password-stdin")
	}
	fmt.Print("Source password: ")
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("read source password: %w", err)
	}
	if len(raw) == 0 {
		return "", errors.New("the source password is empty")
	}
	return string(raw), nil
}

func readAllStdin() (string, error) {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read the source password from stdin: %w", err)
	}
	return string(raw), nil
}

// renderSubscriptionState prints the state the SUBSCRIBER reports. "unknown" is
// its own colour on purpose: it means nobody could ask, which is neither
// healthy nor broken, and collapsing it into either is the failure ADR-172
// exists to prevent.
func renderSubscriptionState(h client.DatabaseSubscriptionHealth) string {
	switch h.State {
	case client.SubscriptionReplicating:
		return lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("● replicating")
	case client.SubscriptionDown:
		down := lipgloss.NewStyle().Bold(true).Foreground(colorDanger).Render("● down")
		if h.Reason != "" {
			return down + mutedStyle.Render(" ("+h.Reason+")")
		}
		return down
	default:
		return lipgloss.NewStyle().Bold(true).Foreground(colorWarning).Render("? unknown")
	}
}

// subscriptionLagBytes renders a byte count at whatever magnitude it lands on.
func subscriptionLagBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for size := n / unit; size >= unit; size /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func subscriptionLag(h client.DatabaseSubscriptionHealth) string {
	if h.ApplyLagBytes == nil {
		return "-"
	}
	return subscriptionLagBytes(*h.ApplyLagBytes)
}

func subscriptionRows(sub *client.DatabaseSubscription) [][]string {
	rows := [][]string{
		{"Name", sub.Name},
		{"Publication", sub.Publication},
		{"Source", fmt.Sprintf("%s@%s:%d/%s", sub.Source.User, sub.Source.Host, sub.Source.Port, sub.Source.DBName)},
		{"SSL mode", orDash(sub.Source.SSLMode)},
		{"State", renderSubscriptionState(sub.Health)},
	}
	if sub.Health.Unreadable != "" {
		rows = append(rows, []string{"Why unknown", sub.Health.Unreadable})
	}
	rows = append(rows,
		[]string{"Apply lag", subscriptionLag(sub.Health)},
		[]string{"Last message", subscriptionTime(sub)},
		[]string{"Apply errors", subscriptionCount(sub.Health.ApplyErrors)},
		[]string{"Sync errors", subscriptionCount(sub.Health.SyncErrors)},
	)
	// `applied` answers whether CREATE SUBSCRIPTION ran, and stays true on a
	// subscription that has since died — shown last, and never as the state.
	rows = append(rows, []string{"Statement applied", strconv.FormatBool(sub.Applied)})
	if sub.Message != "" {
		rows = append(rows, []string{"Operand message", sub.Message})
	}
	return rows
}

func subscriptionTime(sub *client.DatabaseSubscription) string {
	if sub.Health.LastMessageAt == nil {
		return "-"
	}
	return sub.Health.LastMessageAt.Local().Format("2006-01-02 15:04:05")
}

func subscriptionCount(n *int64) string {
	if n == nil {
		return "-"
	}
	return strconv.FormatInt(*n, 10)
}

// registerSubscribeFlags declares the create command's flags on a command. It
// is a function rather than a block in init() so a test can build its own
// command: cobra's AddFlagSet shares the flag VALUES, so a test copying this
// command's set would carry one case's answers into the next.
func registerSubscribeFlags(cmd *cobra.Command) {
	cmd.Flags().String("name", "", "Name for the subscription")
	cmd.Flags().String("from", "", "Source connection URL, e.g. postgres://user@db.example.com:5432/postgres?sslmode=require")
	cmd.Flags().String("publication", "", "Publication to subscribe to on the source")
	cmd.Flags().String("publication-dbname", "", "Database holding the publication, when it is not the one --from names")
	cmd.Flags().Bool("password-stdin", false, "Read the source password from stdin instead of the URL")
	cmd.Flags().StringSlice("parameter", nil, "CREATE SUBSCRIPTION option as key=value (repeatable), e.g. copy_data=false")
}

func init() {
	registerSubscribeFlags(dbSubscribeCmd)
	dbUnsubscribeCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	dbSubscriptionCmd.AddCommand(dbSubscribeCmd, dbSubscriptionListCmd, dbSubscriptionShowCmd, dbUnsubscribeCmd)
	dbCmd.AddCommand(dbSubscriptionCmd)
}
