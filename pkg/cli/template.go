package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

var templateCmd = &cobra.Command{
	Use:     "template",
	Aliases: []string{"templates", "catalog"},
	Short:   "Deploy a self-hosted app from the catalog",
	Long: `The catalog is a curated set of self-hosted apps — analytics, dashboards,
chat, newsletters — each deployed in one command as an app, a managed
database and a bucket the project then owns outright. Nothing stays behind:
what a template makes is what "app create", "db create" and "bucket create"
make, resized, reconfigured and deleted through the same commands.

An app remembers its template. When the catalog carries a newer version,
"fpcloud app get" says so and "fpcloud app upgrade" deploys it as a named
release; deploying an image of your own instead makes the app yours from
then on.`,
}

var templateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the catalog",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFormat := rootCmd.Flag("output").Value.String()
		c := getClient()
		templates, err := c.ListTemplates(context.Background())
		if err != nil {
			return err
		}
		if isStructured(outputFormat) {
			return renderData(templates)
		}
		rows := make([][]string, 0, len(templates))
		for _, t := range templates {
			rows = append(rows, []string{t.Name, t.Version, templateNeeds(t), t.Summary})
		}
		renderTable([]string{"NAME", "VERSION", "CREATES", "SUMMARY"}, rows)
		return nil
	},
}

var templateGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show a catalog entry: what it creates and what it asks for",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFormat := rootCmd.Flag("output").Value.String()
		c := getClient()
		t, err := c.GetTemplate(context.Background(), args[0])
		if err != nil {
			return err
		}
		if isStructured(outputFormat) {
			return renderData(t)
		}
		bold := lipgloss.NewStyle().Bold(true)
		fmt.Printf("%s  %s\n", bold.Render(t.Name), t.Version)
		fmt.Printf("%s\n\n", t.Summary)
		fmt.Printf("Licence:   %s\n", t.Licence)
		fmt.Printf("Upstream:  %s\n", t.Homepage)
		fmt.Printf("Image:     %s\n", t.Image)
		fmt.Printf("Size:      %s cpu, %s memory\n", t.Resources.CPU, t.Resources.Memory)
		fmt.Printf("Creates:   %s\n", templateNeeds(*t))
		if len(t.Inputs) > 0 {
			fmt.Println("\nInputs (--input KEY=VALUE):")
			for _, in := range t.Inputs {
				req := "optional"
				if in.Required {
					req = "required"
				}
				if in.Default != "" {
					req += ", default " + in.Default
				}
				fmt.Printf("  %-28s %s (%s, %s)\n", in.Key, in.Prompt, in.Type, req)
			}
		}
		if t.Notes != "" {
			fmt.Printf("\nAfter deploy: %s\n", t.Notes)
		}
		return nil
	},
}

var templateDeployCmd = &cobra.Command{
	Use:   "deploy <template> --name <app>",
	Short: "Deploy a catalog entry into the current project",
	Long: `Deploy a catalog entry into the current project as an app named --name,
with the managed database (<name>-db) and bucket (<name>) it needs, and
its first release named after the catalog version.

Inputs the entry asks for are given with --input KEY=VALUE; "fpcloud
template get <template>" lists them.

  fpcloud template deploy umami --name stats
  fpcloud template deploy grafana --name grafana --input GF_SECURITY_ADMIN_PASSWORD=...`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFormat := rootCmd.Flag("output").Value.String()
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		name, _ := cmd.Flags().GetString("name")
		if name == "" {
			return fmt.Errorf("--name is required: the app's name, which also names its database (<name>-db) and bucket")
		}
		rawInputs, _ := cmd.Flags().GetStringArray("input")
		inputs := make(map[string]string, len(rawInputs))
		for _, kv := range rawInputs {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return fmt.Errorf("--input %q is not KEY=VALUE", kv)
			}
			inputs[k] = v
		}
		c := getClient()
		var app *client.App
		var deployErr error
		action := func() {
			app, deployErr = c.InstantiateTemplate(context.Background(), projectID, args[0], client.InstantiateTemplateRequest{Name: name, Inputs: inputs})
		}
		if !isStructured(outputFormat) {
			withSpinner("Deploying "+args[0]+"...", action)
		} else {
			action()
		}
		if deployErr != nil {
			return deployErr
		}
		if isStructured(outputFormat) {
			return renderData(app)
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Deploying %s as %s (%s)", args[0], app.Name, app.Release),
		))
		if app.URL != "" {
			fmt.Printf("URL:       %s\n", app.URL)
		}
		if t, err := c.GetTemplate(context.Background(), args[0]); err == nil && t.Notes != "" {
			fmt.Printf("Next:      %s\n", t.Notes)
		}
		fmt.Println("Watch it:  fpcloud project status --watch")
		return nil
	},
}

// templateNeeds is what an entry creates beside its app, in words.
func templateNeeds(t client.Template) string {
	parts := []string{"app"}
	if t.Needs.Database != nil {
		parts = append(parts, "database")
	}
	if t.Needs.Bucket != nil {
		parts = append(parts, "bucket")
	}
	sort.Strings(parts[1:])
	return strings.Join(parts, " + ")
}

func init() {
	templateDeployCmd.Flags().String("name", "", "name of the app to create (required)")
	templateDeployCmd.Flags().StringArray("input", nil, "an input the entry asks for, KEY=VALUE (repeatable)")
	templateCmd.AddCommand(templateListCmd, templateGetCmd, templateDeployCmd)
	rootCmd.AddCommand(templateCmd)
}
