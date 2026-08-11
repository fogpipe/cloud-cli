package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage org secret bundles (Fogpipe Secrets Manager)",
}

var secretsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List org secret bundles",
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		c := getClient()
		var secrets []*client.OrgSecret
		withSpinner("Fetching secrets...", func() {
			secrets, err = c.ListOrgSecrets(cmd.Context(), orgID)
		})
		if err != nil {
			return err
		}
		names := projectNames(cmd, c, orgID)

		headers := []string{"NAME", "KEYS", "TARGETS", "UPDATED"}
		var rows [][]string
		for _, s := range secrets {
			targets := make([]string, 0, len(s.Targets))
			for _, t := range s.Targets {
				targets = append(targets, projectLabel(names, t))
			}
			rows = append(rows, []string{
				s.Name,
				dashIfEmpty(strings.Join(s.Keys, ", ")),
				dashIfEmpty(strings.Join(targets, ", ")),
				s.UpdatedAt.Format("2006-01-02 15:04:05"),
			})
		}
		render(headers, rows, secrets)
		return nil
	},
}

var secretsGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show a secret bundle (use --reveal to show values)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		reveal, _ := cmd.Flags().GetBool("reveal")
		c := getClient()
		var s *client.OrgSecret
		withSpinner("Fetching secret...", func() {
			s, err = c.GetOrgSecret(cmd.Context(), orgID, args[0], reveal)
		})
		if err != nil {
			return err
		}
		// Structured output before anything human: this is the form a
		// `terraform`/`jq` pipeline consumes, and the human block below is lossy
		// for any value containing a newline (an APNs .p8 key, a PEM chain) — a
		// line-based parser cannot put those back together.
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(s)
		}

		names := projectNames(cmd, c, orgID)
		targets := make([]string, 0, len(s.Targets))
		for _, t := range s.Targets {
			targets = append(targets, projectLabel(names, t))
		}

		fmt.Printf("Name:    %s\n", s.Name)
		fmt.Printf("Targets: %s\n", dashIfEmpty(strings.Join(targets, ", ")))
		if reveal {
			fmt.Println("Values:")
			keys := make([]string, 0, len(s.Data))
			for k := range s.Data {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Printf("  %s=%s\n", k, s.Data[k])
			}
		} else {
			fmt.Printf("Keys:    %s\n", dashIfEmpty(strings.Join(s.Keys, ", ")))
		}
		return nil
	},
}

var secretsSetCmd = &cobra.Command{
	Use:   "set <name>",
	Short: "Create or replace a secret bundle",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		dataFlags, _ := cmd.Flags().GetStringArray("data")
		targetFlags, _ := cmd.Flags().GetStringArray("target")
		if len(dataFlags) == 0 {
			return fmt.Errorf("at least one --data KEY=VALUE is required")
		}
		data := make(map[string]string, len(dataFlags))
		for _, kv := range dataFlags {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return fmt.Errorf("invalid --data %q: want KEY=VALUE", kv)
			}
			data[k] = v
		}

		c := getClient()
		targets, err := resolveTargets(cmd, c, orgID, targetFlags)
		if err != nil {
			return err
		}
		var s *client.OrgSecret
		withSpinner("Saving secret...", func() {
			s, err = c.CreateOrgSecret(cmd.Context(), orgID, args[0], data, targets)
		})
		if err != nil {
			return err
		}
		fmt.Println(successBox.Render(fmt.Sprintf(
			"Secret %q saved (%d keys, %d target projects).", s.Name, len(s.Keys), len(s.Targets))))
		return nil
	},
}

var secretsDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a secret bundle",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		c := getClient()
		withSpinner("Deleting secret...", func() {
			err = c.DeleteOrgSecret(cmd.Context(), orgID, args[0])
		})
		if err != nil {
			return err
		}
		fmt.Println(successBox.Render("Secret deleted."))
		return nil
	},
}

// projectNames returns a best-effort project id -> name map for the org.
func projectNames(cmd *cobra.Command, c *client.Client, orgID string) map[string]string {
	names := map[string]string{}
	projects, err := c.ListProjectsInOrg(cmd.Context(), orgID)
	if err != nil {
		return names
	}
	for _, p := range projects {
		names[p.ID] = p.Name
	}
	return names
}

// projectLabel renders a project id as its name when known, else the id.
func projectLabel(names map[string]string, id string) string {
	if n, ok := names[id]; ok {
		return n
	}
	return id
}

// resolveTargets turns --target refs (project name or id) into project ids.
func resolveTargets(cmd *cobra.Command, c *client.Client, orgID string, refs []string) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	projects, err := c.ListProjectsInOrg(cmd.Context(), orgID)
	if err != nil {
		return nil, fmt.Errorf("resolve target projects: %w", err)
	}
	byName := map[string]string{}
	byID := map[string]bool{}
	for _, p := range projects {
		byName[p.Name] = p.ID
		byID[p.ID] = true
	}
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		switch {
		case byID[r]:
			out = append(out, r)
		case byName[r] != "":
			out = append(out, byName[r])
		default:
			return nil, fmt.Errorf("target project %q not found in org", r)
		}
	}
	return out, nil
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func init() {
	secretsGetCmd.Flags().Bool("reveal", false, "Show decrypted values (requires org write)")
	secretsSetCmd.Flags().StringArray("data", nil, "Entry as KEY=VALUE (repeatable)")
	secretsSetCmd.Flags().StringArray("target", nil, "Target project (name or id) to mirror into (repeatable)")
	secretsCmd.AddCommand(secretsListCmd, secretsGetCmd, secretsSetCmd, secretsDeleteCmd)
	rootCmd.AddCommand(secretsCmd)
}
