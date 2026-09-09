package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var fkeNamespaceCmd = &cobra.Command{
	Use:     "namespace",
	Aliases: []string{"ns"},
	Short:   "The Kubernetes namespace behind a project",
}

// Grouped under fke rather than project because a finished pod is only ever
// visible through kubectl: app, project status and the console all render
// workloads, never pods. Someone who has just seen forty Completed rows in
// `kubectl get pods` looks for the cleanup beside the credentials that got them
// there (fogpipe/cloud-workspace#920).
var fkeNamespacePruneCmd = &cobra.Command{
	Use:   "prune [project]",
	Short: "Remove the namespace's finished pods",
	Long: "Deletes pods that have stopped — Succeeded or Failed — and are older than\n" +
		"--older-than. Nothing running, starting or draining is touched.\n\n" +
		"A finished pod is not removed by its own ReplicaSet, so these accumulate\n" +
		"after every rollout and node reboot until something clears them.\n\n" +
		"The platform sweeps every namespace it owns on its own, keeping a full day\n" +
		"so a failure stays readable by whoever has not looked yet. This is the same\n" +
		"operation on demand, with a shorter default, for when you are done with a\n" +
		"namespace now.\n\n" +
		"With no name, the current project's namespace.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// projectSource, not the config directly: this deletes pods, and it read
		// CurrentProject while ignoring --project, so `prune --project other`
		// emptied whatever the config pointed at instead of what was named
		// (fogpipe/cloud-workspace#921). A destructive path selects on the token
		// that names its target (ADR-057).
		project, err := projectSource(args)()
		if err != nil {
			return err
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
	fkeNamespacePruneCmd.Flags().Duration("older-than", 0, "Keep pods that stopped more recently than this (default: the platform's own threshold)")
	fkeNamespacePruneCmd.Flags().Bool("dry-run", false, "Report what would be removed, and remove nothing")
	fkeNamespaceCmd.AddCommand(fkeNamespacePruneCmd)
	fkeCmd.AddCommand(fkeNamespaceCmd)
}
