package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/spf13/cobra"
)

// docEntry is one guide as /docs/index.json describes it.
type docEntry struct {
	Topic    string   `json:"topic"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Surfaces []string `json:"surfaces"`
}

var docsCmd = &cobra.Command{
	Use:   "docs [topic]",
	Short: "Platform guides (quickstart, deploys, databases, storage, ...)",
	Long: `Task-level guides for the platform, read from the platform host — the same
pool served at /llms.txt, so a doc fix reaches you without upgrading the CLI.

With no arguments, lists the available topics. With a topic, prints that guide
as markdown. --all dumps every guide in one stream — made for piping into an
AI agent's context:

  fpcloud docs                  # list topics
  fpcloud docs quickstart       # one guide
  fpcloud docs --all            # everything (pipe me into your agent)

These read unauthenticated endpoints, so they work before you log in, but they
do need to reach the platform. For a dense reference of every command and flag
with no network at all, use --help-llm on any command.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		apiURL := resolveAPIURL()

		all, _ := cmd.Flags().GetBool("all")
		if all {
			body, err := fetchDocs(apiURL, "/llms-full.txt")
			if err != nil {
				return err
			}
			fmt.Print(body)
			return nil
		}

		if len(args) == 1 {
			body, err := fetchDocs(apiURL, "/docs/md/"+args[0]+".md")
			if err != nil {
				return err
			}
			fmt.Print(body)
			return nil
		}

		pool, err := fetchDocsIndex(apiURL)
		if err != nil {
			return err
		}
		pool = slices.DeleteFunc(pool, func(d docEntry) bool {
			return !slices.Contains(d.Surfaces, "cli")
		})
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(pool)
		}
		rows := make([][]string, 0, len(pool))
		for _, d := range pool {
			rows = append(rows, []string{d.Topic, d.Summary})
		}
		renderTable([]string{"TOPIC", "SUMMARY"}, rows)
		fmt.Println(mutedStyle.Render("Read one with `fpcloud docs <topic>`; dump all with `fpcloud docs --all`."))
		return nil
	},
}

// fetchDocs reads one unauthenticated docs path off the platform host.
//
// A 404 is reported as an unknown topic rather than as a transport failure:
// these paths are a closed set, so the only way to miss is to name a guide that
// is not in the pool.
func fetchDocs(apiURL, path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+path, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach %s: %w", apiURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("unknown topic — run `fpcloud docs` to list topics")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s%s returned %d", apiURL, path, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func fetchDocsIndex(apiURL string) ([]docEntry, error) {
	body, err := fetchDocs(apiURL, "/docs/index.json")
	if err != nil {
		return nil, err
	}
	var pool []docEntry
	if err := json.Unmarshal([]byte(body), &pool); err != nil {
		return nil, fmt.Errorf("docs index from %s is not readable: %w", apiURL, err)
	}
	return pool, nil
}

func init() {
	docsCmd.Flags().Bool("all", false, "Print every guide as one markdown stream")
	rootCmd.AddCommand(docsCmd)
}
