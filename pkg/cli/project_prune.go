package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var projectPruneCmd = &cobra.Command{
	Use:   "prune [name]",
	Short: "Remove the project's finished pods",
	Long: "Deletes pods that have stopped — Succeeded or Failed — and are older than\n" +
		"--older-than. Nothing running, starting or draining is touched.\n\n" +
		"A finished pod is not removed by its own ReplicaSet, so these accumulate\n" +
		"after every rollout and node reboot until something clears them.\n\n" +
		"With no name, the current project.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		project := ""
		if len(args) > 0 {
			project = args[0]
		}
		if project == "" {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			project = cfg.CurrentProject
		}
		if project == "" {
			return fmt.Errorf("no current project; name one: fpcloud project prune <name>")
		}

		olderThan, _ := cmd.Flags().GetDuration("older-than")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		res, err := getClient().PrunePods(context.Background(), project, olderThan, dryRun)
		if err != nil {
			return err
		}

		// Nothing matched is a real answer and gets said plainly, rather than
		// rendering as a successful prune of zero.
		if res.Matched == 0 {
			fmt.Println(mutedStyle.Render(fmt.Sprintf(
				"No finished pods older than %s in %q.",
				shortDuration(time.Duration(res.OlderThanSeconds)*time.Second), project)))
			return nil
		}

		breakdown := fmt.Sprintf("%d succeeded, %d failed", res.Succeeded, res.Failed)
		if res.DryRun {
			fmt.Println(mutedStyle.Render(fmt.Sprintf(
				"Would remove %d finished pod(s) from %q (%s). Nothing was deleted.",
				res.Matched, project, breakdown)))
		} else {
			fmt.Println(successBox.Render(
				lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
					fmt.Sprintf(" Removed %d finished pod(s) from %q (%s).", res.Pruned, project, breakdown),
			))
		}
		// The server bounds one request's work, so what it left is stated rather
		// than left to look like nothing was there.
		if res.Remaining > 0 {
			fmt.Println(mutedStyle.Render(fmt.Sprintf(
				"  %d more matched than one request removes — run it again.", res.Remaining)))
		}
		return nil
	},
}

func init() {
	projectPruneCmd.Flags().Duration("older-than", 0, "Keep pods that stopped more recently than this (default: the platform's own threshold)")
	projectPruneCmd.Flags().Bool("dry-run", false, "Report what would be removed, and remove nothing")
	projectCmd.AddCommand(projectPruneCmd)
}
