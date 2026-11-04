package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Show the resolved CLI context (org, project, api-url, identity)",
	Long: `Print the context the CLI resolves for commands: current organization,
project, API URL, and logged-in identity — and, when signed in, which credential
authenticates you (an API key or the browser login) and, for a browser login,
the factors you sign in with.

Values follow the usual precedence — an explicit --org/--project flag wins over
the config file, which wins over the built-in default.

Config and Token say which files answered, so a directory that keeps its own
state (fpcloud init) shows it. They are resolved one file at a time: a local
config.yaml beside a global token is the ordinary case.

An org or project the identity cannot reach is said to be, since every command
under it would fail. A project that does not exist and one you hold no role on
are the same answer.

  fpcloud context                     # human-readable summary
  fpcloud context -o json             # machine-readable
  fpcloud context --format '{org}/{project}'   # terse, for shell prompts

The --format template understands {org}, {project}, {api_url}, and {identity}.
Identity is only looked up (a network call) when the default/json output is used
or {identity} appears in --format, so prompt use stays fast.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		org := rootCmd.Flag("org").Value.String()
		project := rootCmd.Flag("project").Value.String()
		apiURL := resolveAPIURL()

		if format, _ := cmd.Flags().GetString("format"); format != "" {
			out := format
			out = strings.ReplaceAll(out, "{org}", org)
			out = strings.ReplaceAll(out, "{project}", project)
			out = strings.ReplaceAll(out, "{api_url}", apiURL)
			if strings.Contains(out, "{identity}") {
				out = strings.ReplaceAll(out, "{identity}", currentIdentity())
			}
			fmt.Println(out)
			return nil
		}

		identity := currentIdentity()
		// Whether the identity can see the org the context names. A context
		// that names an org the identity cannot reach renders like a working
		// one and fails on every command; saying so here is the difference.
		access := "unknown"
		if identity != "" && org != "" {
			orgs, err := getClient().ListOrgs(context.Background())
			if err == nil {
				access = "no"
				if matchOrg(orgs, org) != nil {
					access = "yes"
				}
			}
		}
		// The same question of the project, asked of the read every project
		// command starts from. Only when the org itself is reachable: a project
		// under an org the identity is not in fails for the org's reason.
		projectAccess := "unknown"
		if access == "yes" && project != "" {
			_, err := getClient().GetProject(context.Background(), project)
			projectAccess = projectAccessOf(err)
		}

		credential, signIn := "", ""
		if identity != "" {
			credential, signIn = currentCredential()
		}

		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(map[string]string{
				"org":            org,
				"project":        project,
				"api_url":        apiURL,
				"identity":       identity,
				"credential":     credential,
				"mfa":            signIn,
				"org_access":     access,
				"project_access": projectAccess,
				"config_path":    configPath(),
				"token_path":     tokenCachePath(),
			})
		}

		orgShown := orDefault(org, "(unset)")
		if access == "no" {
			orgShown = org + " " + mutedStyle.Render("("+identity+" is not a member; run fpcloud switch)")
		}
		projectShown := orDefault(project, "(unset)")
		if projectAccess == "no" {
			projectShown = project + " " + mutedStyle.Render("(not reachable as "+identity+"; run fpcloud project switch)")
		}
		rows := [][]string{
			{"Organization", orgShown},
			{"Project", projectShown},
			{"API URL", apiURL},
			{"Identity", orDefault(identity, "(unauthenticated)")},
		}
		if credential != "" {
			rows = append(rows, []string{"Credential", credential})
		}
		if signIn != "" {
			rows = append(rows, []string{"MFA", signIn})
		}
		rows = append(rows,
			[]string{"Config", configPath()},
			[]string{"Token", tokenCachePath()},
		)
		fmt.Println(renderInfoBox("Context", rows))
		return nil
	},
}

// projectAccessOf reads the answer to reading the context's project. A refusal
// and a missing project are one answer on purpose: the API does not say which,
// so neither does this. Anything else, an unreachable control plane included,
// says nothing about the project.
func projectAccessOf(err error) string {
	if err == nil {
		return "yes"
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) &&
		(apiErr.StatusCode == http.StatusForbidden || apiErr.StatusCode == http.StatusNotFound) {
		return "no"
	}
	return "unknown"
}

// currentCredential names what authenticates the CLI right now — a static API
// key, or the browser login — and, for a browser login, how the person signs
// in at the identity provider. Only a browser login has factors; an API key is
// the credential itself. A provider the platform cannot ask answers "", shown
// as nothing rather than read as no second factor.
func currentCredential() (credential, signIn string) {
	if flag := rootCmd.Flag("api-key"); flag.Changed || envAPIKey() != "" {
		return "API key", ""
	}
	if podIdentityFile() != "" {
		return "pod identity token", ""
	}
	if rootCmd.Flag("api-key").Value.String() != "" {
		return "API key", ""
	}
	credential = "browser login"
	if sec, err := getClient().AuthSecurity(context.Background()); err == nil {
		signIn = sec.MFA
		if len(sec.Methods) > 0 {
			signIn += " (" + strings.Join(sec.Methods, ", ") + ")"
		}
	}
	return credential, signIn
}

// currentIdentity returns the logged-in user's email, or "" if the CLI is
// unauthenticated or the control plane is unreachable. It never errors — the
// resolved context is still useful without a live identity.
func currentIdentity() string {
	me, err := getClient().GetMe(context.Background())
	if err != nil {
		return ""
	}
	// Whichever principal the credential is: a CI shell authenticated as a
	// service account has an identity worth showing, and blanking it reads as
	// "not logged in".
	return me.Email()
}

// orDefault returns s, or def when s is empty (with muted styling for display).
func orDefault(s, def string) string {
	if s == "" {
		return mutedStyle.Render(def)
	}
	return s
}

func init() {
	contextCmd.Flags().String("format", "", "Terse template output, e.g. '{org}/{project}' (fields: {org} {project} {api_url} {identity})")
	rootCmd.AddCommand(contextCmd)
}
