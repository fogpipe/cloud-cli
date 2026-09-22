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

The newest few are shown, and the footer says how to read further:
` + "`--all`" + ` for the whole history, or ` + "`--before <version>`" + ` for the page
behind one you can see.

Needs no account: what shipped in a release is a published fact, like ` + "`fpcloud billing prices --published`" + `.

` + "`fpcloud version`" + ` says which version you are on and which one to pin; this says
what is in it.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")
		before, _ := cmd.Flags().GetString("before")
		all, _ := cmd.Flags().GetBool("all")
		c := getClient()

		want := ""
		if len(args) > 0 {
			want = args[0]
		}
		// A named version is read by walking the changelog rather than asked
		// for: the platform serves the listing and nothing else, and a version
		// old enough to be past the first page is exactly the one somebody
		// names. Bounded by the changelog itself, in pages of the size asked
		// for (fogpipe/cloud-workspace#1054).
		readAll := all || want != ""

		var releases []client.Release
		var err error
		hasMore := false
		withSpinner("Fetching the changelog...", func() {
			cursor := before
			for {
				var log *client.Changelog
				if log, err = c.Changelog(cmd.Context(), limit, cursor); err != nil {
					return
				}
				releases = append(releases, log.Releases...)
				hasMore = log.HasMore
				if !readAll || !hasMore || len(log.Releases) == 0 {
					return
				}
				cursor = log.Releases[len(log.Releases)-1].Version
			}
		})
		if err != nil {
			return err
		}

		if want != "" {
			match := []client.Release{}
			for _, r := range releases {
				if r.Version == want {
					match = append(match, r)
				}
			}
			if len(match) == 0 {
				return fmt.Errorf("no release notes for %s", want)
			}
			releases, hasMore = match, false
		}

		if isStructured(rootCmd.Flag("output").Value.String()) {
			render(nil, nil, client.Changelog{Releases: releases, HasMore: hasMore})
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
			// A release with no notes is a release that changed nothing you can
			// see, and prints as its version and date alone
			// (fogpipe/cloud-workspace#1052).
			if notes := strings.TrimRight(r.Notes, "\n"); notes != "" {
				for _, line := range strings.Split(notes, "\n") {
					fmt.Println("  " + line)
				}
			}
		}
		// Said rather than left to be discovered: a full page and the end of
		// the changelog look the same from here.
		if hasMore {
			fmt.Println()
			fmt.Println(mutedStyle.Render(fmt.Sprintf(
				"  Older releases: fpcloud changelog --before %s, or --all",
				releases[len(releases)-1].Version)))
		}
		return nil
	},
}

func init() {
	changelogCmd.Flags().Int("limit", 0, "How many releases to show at a time (default: the platform's own)")
	changelogCmd.Flags().String("before", "", "Start from the release older than this one")
	changelogCmd.Flags().Bool("all", false, "Read every page, back to the first release")
	rootCmd.AddCommand(changelogCmd)
}
