package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Keep this directory's fpcloud state — context and login — to itself",
	Long: `Create ./.fpcloud/, the state directory every fpcloud command below here
resolves against.

The CLI finds its state by walking up from the working directory to the nearest
.fpcloud/, falling back to ~/.fpcloud one file at a time. So a directory that
holds only a config.yaml has its own organization and project and goes on using
your global login, and one you also log in from has its own identity — which is
how two people, or two accounts, work on one machine at the same time.

There is nothing to init globally: ~/.fpcloud is written the first time a
command has something to put there.

  fpcloud init && fpcloud switch acme web                 # this directory's context
  fpcloud init && fpcloud login --account you@example.com # …and its own identity

Name the account. A bare "fpcloud login" asks the issuer for a picker, and if
it signs you in as whoever it already had, nothing refuses it — the wrong
identity is refused later, per request, by the org it is not a member of, as
"forbidden" with nothing pointing back at the login that chose it.

A token written here is a token inside your checkout — ignore .fpcloud/ in
version control.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir := filepath.Join(wd, stateDirName)
		if fi, err := os.Stat(dir); err == nil {
			if !fi.IsDir() {
				return fmt.Errorf("%s exists and is not a directory", dir)
			}
			fmt.Println(mutedStyle.Render(dir + " already exists — this directory already keeps its own state."))
			return nil
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" %s — fpcloud state here is now this directory's.", dir),
		))
		fmt.Println(mutedStyle.Render("Add .fpcloud/ to .gitignore: a login from here writes its token into it."))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
