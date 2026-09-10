package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var appCVEsCmd = &cobra.Command{
	Use:     "cves [name]",
	Aliases: []string{"cve", "vulnerabilities"},
	Short:   "List the CVEs in the image an app is running",
	Long: `List the CVEs in the image an app is running right now.

Answered against the app's live image digest, not a tag: a tag moves, and the
digest is what the app is actually running.

Each row is one CVE in one package: the version the image carries, and the
version that fixes it — empty when no upstream fix exists, which is the row to
assess for reachability rather than bump. Most severe first, then by package.

An empty list is only a clean result when the state says "scanned". An image
outside the Fogpipe registry, or one nothing has scanned yet, reports that
state instead — it is not reported as clean.

--min-severity narrows to what is worth acting on: a floor, not a set, so
--min-severity high is CRITICAL and HIGH. UNKNOWN ranks LOWEST and is excluded
by any floor. The state line above the table always counts the WHOLE image, so
a filter cannot turn an image with criticals in it into one reporting none.

  fpcloud app cves web
  fpcloud app cves web --min-severity high
  fpcloud app cves web -o json`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		appID, err := appIDFrom(c, cmd, args)
		if err != nil {
			return err
		}
		list, err := c.ListAppCVEs(context.Background(), appID)
		if err != nil {
			return err
		}

		level, _ := cmd.Flags().GetString("min-severity")
		total := len(list.CVEs)
		kept, hidden, err := filterBySeverity(list.CVEs, level)
		if err != nil {
			return err
		}
		list.CVEs = kept

		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(list)
		}

		// The state goes to stdout, above the table, not to stderr — same line
		// and same words as `registry cves`, which answers the same question
		// about an image (#934). On stderr a piped read would carry an empty
		// table and nothing saying the emptiness was never a measurement.
		fmt.Printf("%s  %s\n", list.AppName, mutedStyle.Render(list.Image))
		if list.Digest != "" && list.Digest != list.Image {
			fmt.Println(mutedStyle.Render(list.Digest))
		}
		// The TOTAL, never the filtered count -- see registry cves, which shares
		// this line and the reason (fogpipe/cloud-workspace#951).
		fmt.Println(scanStateLine(list.State, list.Reason, list.ScannedAt, total))
		if line := severityFilterLine(level, len(kept), hidden); line != "" {
			fmt.Println(line)
		}
		if len(list.CVEs) == 0 {
			return nil
		}

		var rows [][]string
		for _, cve := range list.CVEs {
			if len(cve.Packages) == 0 {
				rows = append(rows, []string{cve.Severity, cve.ID, "", "", "", cve.Title})
				continue
			}
			for _, p := range cve.Packages {
				rows = append(rows, []string{cve.Severity, cve.ID, p.Name, p.InstalledVersion, p.FixedVersion, cve.Title})
			}
		}
		fmt.Println()
		renderTable([]string{"SEVERITY", "CVE", "PACKAGE", "INSTALLED", "FIXED", "TITLE"}, rows)
		return nil
	},
}
