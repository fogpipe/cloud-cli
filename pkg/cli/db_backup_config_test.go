package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestBackupConfigChangeSendsOnlyWhatWasPassed(t *testing.T) {
	newCmd := func(args ...string) *cobra.Command {
		cmd := &cobra.Command{Use: "set"}
		addBackupConfigSetFlags(cmd)
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		return cmd
	}

	config, err := backupConfigChange(newCmd("--retention", "14d"))
	if err != nil {
		t.Fatal(err)
	}
	if config.Retention == nil || *config.Retention != "14d" {
		t.Fatalf("retention = %v, want 14d", config.Retention)
	}
	if config.Schedule != nil || config.Enabled != nil {
		t.Fatalf("retention alone sent schedule=%v enabled=%v; both must stay unset", config.Schedule, config.Enabled)
	}

	config, err = backupConfigChange(newCmd("--disable"))
	if err != nil {
		t.Fatal(err)
	}
	if config.Enabled == nil || *config.Enabled {
		t.Fatalf("--disable sent enabled=%v, want false", config.Enabled)
	}

	if _, err := backupConfigChange(newCmd()); err == nil {
		t.Fatal("no flag at all must be refused, not sent as an empty change")
	}
	if _, err := backupConfigChange(newCmd("--disable", "--schedule", "0 3 * * 0")); err == nil {
		t.Fatal("--disable with --schedule must be refused")
	}
}
