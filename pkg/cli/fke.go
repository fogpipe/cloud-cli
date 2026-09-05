package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

var fkeCmd = &cobra.Command{
	Use:   "fke",
	Short: "Fogpipe Kubernetes Engine — scoped kubectl access to your projects",
	Long: "Fogpipe Kubernetes Engine (FKE) gives kubectl access scoped to your own\n" +
		"namespaces. `fke get-credentials` writes a kubeconfig context whose token is\n" +
		"minted per-call: confined to one project by default (create/read/update/delete\n" +
		"on your workloads; other namespaces are invisible), or with `--scope org` to\n" +
		"every project namespace your organization owns. Requires the FKE entitlement\n" +
		"on your organization (operator-granted — contact Fogpipe to request it).",
}

// fkeScope is what a kubeconfig context reaches: one project's namespace, or
// every namespace the organization owns.
type fkeScope string

const (
	fkeScopeProject fkeScope = "project"
	fkeScopeOrg     fkeScope = "org"
)

func fkeScopeFlag(cmd *cobra.Command) (fkeScope, error) {
	v, _ := cmd.Flags().GetString("scope")
	switch fkeScope(v) {
	case fkeScopeProject, fkeScopeOrg:
		return fkeScope(v), nil
	}
	return "", usageError{fmt.Errorf("--scope must be %q or %q, got %q", fkeScopeProject, fkeScopeOrg, v)}
}

var fkeGetCredentialsCmd = &cobra.Command{
	Use:   "get-credentials",
	Short: "Write a kubeconfig context for kubectl access, scoped to a project or to your whole organization",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		kubePath, _ := cmd.Flags().GetString("kubeconfig")
		scope, err := fkeScopeFlag(cmd)
		if err != nil {
			return err
		}

		var entry kubeconfigEntry
		switch scope {
		case fkeScopeOrg:
			org, err := fkeOrgRef(cmd)
			if err != nil {
				return err
			}
			entry, err = orgCredentialsEntry(ctx, org)
			if err != nil {
				return err
			}
		default:
			project, err := requireProject()
			if err != nil {
				return err
			}
			entry, err = credentialsEntry(ctx, project)
			if err != nil {
				return err
			}
		}
		written, err := writeKubeconfig(kubePath, entry)
		if err != nil {
			return err
		}
		fmt.Println(successBox.Render(fmt.Sprintf("✓ Wrote context %q to %s", entry.context, written)))
		if entry.namespace != "" {
			fmt.Println(mutedStyle.Render(fmt.Sprintf("  Namespace %q is now current. Try:  kubectl get pods", entry.namespace)))
		} else {
			fmt.Println(mutedStyle.Render("  Every namespace your organization owns is reachable; none is current. Try:  kubectl get pods -n <namespace>"))
		}
		return nil
	},
}

// fkeOrgRef is the organization an org-scoped context is for: --org (or the
// current org from `fpcloud switch`, which is the flag's default) as written,
// since the API resolves an id, a short id or a display name alike; with
// nothing set, the caller's only org.
func fkeOrgRef(cmd *cobra.Command) (string, error) {
	if org := rootCmd.Flag("org").Value.String(); org != "" {
		return org, nil
	}
	return resolveOrgID(cmd)
}

// credentialsEntry resolves the kubeconfig entry for a project: it asks the API
// for the cluster connection facts (endpoint + CA) and builds a scoped context
// whose token is minted per-call by `fke get-token --project <project>`. project
// may be a name or id (the API resolves it).
func credentialsEntry(ctx context.Context, project string) (kubeconfigEntry, error) {
	creds, err := getClient().FKECredentials(ctx, project)
	if err != nil {
		return kubeconfigEntry{}, err
	}
	return kubeconfigEntryFrom(creds, []string{"fke", "get-token", "--project", project})
}

// orgCredentialsEntry is credentialsEntry for the org-wide context: one
// identity bound into every project namespace the org owns, minted per-call by
// `fke get-token --scope org --org <org>`. The org is pinned into the exec block
// as written, so the context keeps naming the org it was made for whatever
// `fpcloud switch` later selects.
func orgCredentialsEntry(ctx context.Context, org string) (kubeconfigEntry, error) {
	creds, err := getClient().OrgFKECredentials(ctx, org)
	if err != nil {
		return kubeconfigEntry{}, err
	}
	return kubeconfigEntryFrom(creds, []string{"fke", "get-token", "--scope", string(fkeScopeOrg), "--org", org})
}

func kubeconfigEntryFrom(creds *client.ClusterCredentials, execArgs []string) (kubeconfigEntry, error) {
	caData, err := base64.StdEncoding.DecodeString(creds.CertificateAuthorityData)
	if err != nil {
		return kubeconfigEntry{}, fmt.Errorf("decode cluster CA: %w", err)
	}
	return kubeconfigEntry{
		context:   creds.Context,
		server:    creds.Server,
		caData:    caData,
		namespace: creds.Namespace,
		execArgs:  execArgs,
	}, nil
}

var fkeGetTokenCmd = &cobra.Command{
	Use:    "get-token",
	Short:  "Print a kubectl ExecCredential with a project- or org-scoped token (used by the kubeconfig; not for direct use)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		scope, err := fkeScopeFlag(cmd)
		if err != nil {
			return err
		}
		var tok *client.ClusterToken
		switch scope {
		case fkeScopeOrg:
			org, err := fkeOrgRef(cmd)
			if err != nil {
				return err
			}
			tok, err = getClient().OrgFKEToken(context.Background(), org)
			if err != nil {
				return err
			}
		default:
			project, err := requireProject()
			if err != nil {
				return err
			}
			tok, err = getClient().FKEToken(context.Background(), project)
			if err != nil {
				return err
			}
		}
		expiry := tok.ExpirationTimestamp
		if expiry == "" {
			expiry = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		}
		out := map[string]any{
			"apiVersion": "client.authentication.k8s.io/v1",
			"kind":       "ExecCredential",
			"status": map[string]any{
				"token":               tok.Token,
				"expirationTimestamp": expiry,
			},
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	},
}

func init() {
	fkeGetCredentialsCmd.Flags().String("kubeconfig", "", "kubeconfig file to write (default: $KUBECONFIG or ~/.kube/config)")
	for _, c := range []*cobra.Command{fkeGetCredentialsCmd, fkeGetTokenCmd} {
		c.Flags().String("scope", string(fkeScopeProject), "What the context reaches: \"project\" (the current project's namespace) or \"org\" (every project namespace the organization owns)")
	}
	fkeCmd.AddCommand(fkeGetCredentialsCmd, fkeGetTokenCmd)
	// Hide the whole tree unless the current org is FKE-entitled (cached on `org
	// use`). This is UX only — executing a hidden command still fails closed
	// server-side (403) for a non-entitled org.
	if cfg, err := loadConfig(); err == nil && cfg != nil {
		fkeCmd.Hidden = !cfg.CurrentOrgFKE
	}
	rootCmd.AddCommand(fkeCmd)
}
