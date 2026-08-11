package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func newAppRefCmd() *cobra.Command {
	c := &cobra.Command{Use: "x"}
	c.Flags().String("app", "", "")
	return c
}

func TestAppRefFrom(t *testing.T) {
	t.Run("flag wins over positional", func(t *testing.T) {
		c := newAppRefCmd()
		if err := c.Flags().Set("app", "flagapp"); err != nil {
			t.Fatal(err)
		}
		if got := appRefFrom(c, []string{"posapp"}); got != "flagapp" {
			t.Errorf("got %q, want flagapp", got)
		}
	})
	t.Run("positional shorthand when no flag", func(t *testing.T) {
		c := newAppRefCmd()
		if got := appRefFrom(c, []string{"posapp"}); got != "posapp" {
			t.Errorf("got %q, want posapp", got)
		}
	})
	t.Run("empty when neither given", func(t *testing.T) {
		c := newAppRefCmd()
		if got := appRefFrom(c, nil); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}
