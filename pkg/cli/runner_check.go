package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// workflowDir is where GitHub keeps them, and the only place this looks when
// no --workflow is given.
const workflowDir = ".github/workflows"

var runnerCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Say which jobs in a workflow can run on this project's runner",
	Long: `Read workflow files and say, before you push, which jobs this project's
runner can actually take.

With no --workflow it reads every file in .github/workflows relative to the
working directory. The files are sent as text and compared against this
project's runner label; nothing reads your repository, which is what lets this
answer for a workflow you have not pushed yet.

Each job gets one verdict:

  runs           the label is this project's and the job asks for nothing
                 the runner cannot serve
  queues         the label is nobody's here, so the job waits forever
  refused        the label is this project's, but the job declares something
                 the runner cannot serve
  github         a GitHub-hosted label: it runs, on GitHub's minutes
  undetermined   runs-on is an expression, so which runner it picks is not
                 knowable from the text

  fpcloud runner check
  fpcloud runner check --workflow .github/workflows/ci.yml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := requireProject()
		if err != nil {
			return err
		}
		paths, err := cmd.Flags().GetStringSlice("workflow")
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			paths, err = discoverWorkflows(workflowDir)
			if err != nil {
				return err
			}
			if len(paths) == 0 {
				return fmt.Errorf("no workflows in %s — run this from the repository root, or name one with --workflow", workflowDir)
			}
		}
		req := client.CheckRunnerWorkflowsRequest{}
		for _, p := range paths {
			content, err := os.ReadFile(p)
			if err != nil {
				return fmt.Errorf("read %s: %w", p, err)
			}
			req.Workflows = append(req.Workflows, client.WorkflowSource{Path: p, Content: string(content)})
		}

		c := getClient()
		out, err := c.CheckRunnerWorkflows(context.Background(), project, req)
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(out)
		}

		rows := make([][]string, len(out.Jobs))
		for i, j := range out.Jobs {
			rows[i] = []string{j.Workflow, j.Job, runsOnCell(j.RunsOn), j.Verdict, j.Reason}
		}
		render([]string{"WORKFLOW", "JOB", "RUNS-ON", "VERDICT", "WHY"}, rows, out.Jobs)
		fmt.Println()
		fmt.Println(mutedStyle.Render(checkLabelNote(out.Label)))
		fmt.Println()
		return nil
	},
}

// discoverWorkflows lists the workflow files in dir. GitHub reads .yml and
// .yaml there and nothing else, so neither does this.
func discoverWorkflows(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch filepath.Ext(e.Name()) {
		case ".yml", ".yaml":
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

func runsOnCell(labels []string) string {
	switch len(labels) {
	case 0:
		return "—"
	case 1:
		return labels[0]
	default:
		return fmt.Sprintf("%s (+%d)", labels[0], len(labels)-1)
	}
}

// checkLabelNote names what the workflows were compared against. A project
// with no runner would otherwise read as a workflow full of errors rather than
// as a project that has not declared its runner yet.
func checkLabelNote(label string) string {
	if label == "" {
		return "This project has no runner, so every self-hosted label queues.\nDeclare it with `fpcloud runner create`."
	}
	return "Compared against this project's runner label: " + label
}

func init() {
	runnerCheckCmd.Flags().StringSlice("workflow", nil, "Workflow file to check (repeatable; default: every file in .github/workflows)")
	runnerCmd.AddCommand(runnerCheckCmd)
}
