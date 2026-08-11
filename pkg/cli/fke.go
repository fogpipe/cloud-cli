package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var fkeCmd = &cobra.Command{
	Use:   "fke",
	Short: "Fogpipe Kubernetes Engine — scoped kubectl access to your projects",
	Long: "Fogpipe Kubernetes Engine (FKE) gives kubectl access scoped to a project's\n" +
		"namespace. `fke get-credentials` writes a kubeconfig context whose token is\n" +
		"minted per-call, confined to your project (create/read/update/delete on your\n" +
		"workloads; other namespaces are invisible). Requires the FKE entitlement on\n" +
		"your organization (operator-granted — contact Fogpipe to request it).",
}

var fkeGetCredentialsCmd = &cobra.Command{
	Use:   "get-credentials",
	Short: "Write a project-scoped kubeconfig context for kubectl access",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		kubePath, _ := cmd.Flags().GetString("kubeconfig")

		project, err := requireProject()
		if err != nil {
			return err
		}
		entry, err := credentialsEntry(ctx, project)
		if err != nil {
			return err
		}
		written, err := writeKubeconfig(kubePath, entry)
		if err != nil {
			return err
		}
		fmt.Println(successBox.Render(fmt.Sprintf("✓ Wrote context %q to %s", entry.context, written)))
		fmt.Println(mutedStyle.Render(fmt.Sprintf("  Namespace %q is now current. Try:  kubectl get pods", entry.namespace)))
		return nil
	},
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
	caData, err := base64.StdEncoding.DecodeString(creds.CertificateAuthorityData)
	if err != nil {
		return kubeconfigEntry{}, fmt.Errorf("decode cluster CA: %w", err)
	}
	return kubeconfigEntry{
		context:   creds.Context,
		server:    creds.Server,
		caData:    caData,
		namespace: creds.Namespace,
		execArgs:  []string{"fke", "get-token", "--project", project},
	}, nil
}

var fkeGetTokenCmd = &cobra.Command{
	Use:    "get-token",
	Short:  "Print a kubectl ExecCredential with a project-scoped token (used by the kubeconfig; not for direct use)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := requireProject()
		if err != nil {
			return err
		}
		tok, err := getClient().FKEToken(context.Background(), project)
		if err != nil {
			return err
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
	fkeCmd.AddCommand(fkeGetCredentialsCmd, fkeGetTokenCmd)
	// Hide the whole tree unless the current org is FKE-entitled (cached on `org
	// use`). This is UX only — executing a hidden command still fails closed
	// server-side (403) for a non-entitled org.
	if cfg, err := loadConfig(); err == nil && cfg != nil {
		fkeCmd.Hidden = !cfg.CurrentOrgFKE
	}
	rootCmd.AddCommand(fkeCmd)
}
