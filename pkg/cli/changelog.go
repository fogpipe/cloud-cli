package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

var changelogCmd = &cobra.Command{
	Use:   "changelog [version]",
	Short: "Show what changed in each release",
	Long: `What changed in each release of the product, newest first.

One version number names one release of the whole product — the CLI, the control
plane and the OpenTofu provider carry the same tag — so one entry covers all
three. Name a version to read that one.

Needs no account: what shipped in a release is a published fact, like ` + "`fpcloud billing prices --published`" + `.

` + "`fpcloud version`" + ` says which version you are on and which one to pin; this says
what is in it.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")
		c := getClient()

		var log *client.Changelog
		var err error
		withSpinner("Fetching the changelog...", func() {
			log, err = c.Changelog(cmd.Context(), limit)
		})
		if err != nil {
			return err
		}

		releases := log.Releases
		if len(args) > 0 {
			// Filtered here rather than asked for by version: the listing is
			// already bounded and the whole answer is a handful of rows, so one
			// route serves both and there is no second shape to keep in step.
			want := args[0]
			releases = nil
			for _, r := range log.Releases {
				if r.Version == want {
					releases = append(releases, r)
				}
			}
			if len(releases) == 0 {
				return fmt.Errorf("no release notes for %s", want)
			}
		}

		if isStructured(rootCmd.Flag("output").Value.String()) {
			render(nil, nil, client.Changelog{Releases: releases})
			return nil
		}

		if len(releases) == 0 {
			fmt.Println(mutedStyle.Render("  No release notes recorded yet."))
			return nil
		}

		for i, r := range releases {
			if i > 0 {
				fmt.Println()
			}
			fmt.Println(titleStyle.Render(r.Version) + mutedStyle.Render("  "+r.ReleasedAt.Format("2006-01-02")))
			// A release with no notes is a release that changed nothing you
			// can see, and prints as its version and date alone
			// (fogpipe/cloud-workspace#1052).
			if notes := strings.TrimRight(r.Notes, "\n"); notes != "" {
				for _, line := range strings.Split(notes, "\n") {
					fmt.Println("  " + line)
				}
			}
		}
		return nil
	},
}

func init() {
	changelogCmd.Flags().Int("limit", 0, "How many releases to show (default: the platform's own)")
	rootCmd.AddCommand(changelogCmd)
}
