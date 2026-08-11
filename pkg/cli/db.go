package cli

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

var dbCmd = &cobra.Command{
	Use:     "db",
	Aliases: []string{"dbs", "database", "databases"},
	Short:   "Manage databases",
}

// resolveDatabaseID turns a user-supplied database reference (name or id) into a
// database id. A UUID is returned as-is (and needs no project); a plain name is
// looked up in the current project, where database names are unique.
func resolveDatabaseID(c *client.Client, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("database name or id is required")
	}
	if looksLikeUUID(ref) {
		return ref, nil
	}
	project, err := requireProject()
	if err != nil {
		return "", fmt.Errorf("resolve database %q: %w", ref, err)
	}
	dbs, err := c.ListDatabases(context.Background(), project)
	if err != nil {
		return "", err
	}
	for _, d := range dbs {
		if d.Name == ref {
			return d.ID, nil
		}
	}
	return "", fmt.Errorf("database %q not found in project %q", ref, project)
}

var dbCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new PostgreSQL database",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		version, _ := cmd.Flags().GetString("version")
		displayName, _ := cmd.Flags().GetString("display-name")
		cpu, _ := cmd.Flags().GetString("cpu")
		memory, _ := cmd.Flags().GetString("memory")
		storage, _ := cmd.Flags().GetString("storage")
		pooler, _ := cmd.Flags().GetBool("pooler")

		outputFormat := rootCmd.Flag("output").Value.String()
		c := getClient()

		var db *client.Database
		var createErr error
		action := func() {
			db, createErr = c.CreateDatabase(context.Background(), projectID, client.CreateDatabaseRequest{
				Name:        args[0],
				DisplayName: displayName,
				Engine:      "postgres",
				Version:     version,
				CPU:         cpu,
				Memory:      memory,
				Storage:     storage,
				Pooler:      pooler,
			})
		}

		if !isStructured(outputFormat) {
			withSpinner("Creating database...", action)
		} else {
			action()
		}
		if createErr != nil {
			return createErr
		}

		if isStructured(outputFormat) {
			return renderData(db)
		}

		pairs := [][]string{
			{"ID", mutedStyle.Render(db.ID)},
			{"Name", db.Name},
			{"Display Name", db.DisplayName},
			{"Engine", db.Engine},
			{"Version", db.Version},
			{"Status", renderStatus(db.Status)},
		}
		if addr := dbAddress(db); addr != "" {
			pairs = append(pairs, []string{"Address", addr})
		}
		if db.Password != "" {
			// Shown once, at create: CNPG rotates the app role out of band, so this
			// record's password goes stale and is never returned again.
			pairs = append(pairs, []string{"Username", db.Username})
			pairs = append(pairs, []string{"Password", lipgloss.NewStyle().Bold(true).Foreground(colorInfo).Render(db.Password)})
		}
		fmt.Println(renderInfoBox("Database Created", pairs))
		return nil
	},
}

var dbListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all databases in the current project",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}

		c := getClient()
		dbs, err := c.ListDatabases(context.Background(), projectID)
		if err != nil {
			return err
		}

		rows := make([][]string, len(dbs))
		for i, db := range dbs {
			rows[i] = []string{db.ID, db.Name, db.Engine, db.Version, db.Plan, renderStatus(db.Status)}
		}
		render([]string{"ID", "NAME", "ENGINE", "VERSION", "PLAN", "STATUS"}, rows, dbs)
		return nil
	},
}

var dbGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show database details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveDatabaseID(c, args[0])
		if err != nil {
			return err
		}
		db, err := c.GetDatabase(context.Background(), id)
		if err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(db)
		}

		addr := dbAddress(db)
		if addr == "" {
			addr = mutedStyle.Render("not yet available")
		}

		fmt.Println(renderInfoBox("Database Details", [][]string{
			{"ID", mutedStyle.Render(db.ID)},
			{"Name", db.Name},
			{"Display Name", db.DisplayName},
			{"Engine", db.Engine},
			{"Version", db.Version},
			{"Status", renderStatus(db.Status)},
			{"Address", addr},
			{"Username", orDash(db.Username)},
		}))
		fmt.Println()
		fmt.Println(mutedStyle.Render("  Credentials rotate; get a live connection with: fpcloud db connect " + db.Name))
		return nil
	},
}

var dbDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a database",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			var confirm bool
			err := huh.NewConfirm().
				Title(fmt.Sprintf("Delete database %q?", args[0])).
				Description("This action cannot be undone. All data will be lost.").
				Affirmative("Yes, delete").
				Negative("Cancel").
				Value(&confirm).
				Run()
			if err != nil {
				return err
			}
			if !confirm {
				fmt.Println(mutedStyle.Render("Aborted."))
				return nil
			}
		}

		c := getClient()
		id, err := resolveDatabaseID(c, args[0])
		if err != nil {
			return err
		}
		if err := c.DeleteDatabase(context.Background(), id); err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Database %q deleted.", args[0]),
		))
		return nil
	},
}

var dbBackupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Manage database backups",
}

var dbBackupCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Trigger a manual backup (--external backs up to your own bucket)",
	Args:  cobra.ExactArgs(1),
	RunE:  runDBBackupCreate,
}

var dbBackupListCmd = &cobra.Command{
	Use:     "list <name>",
	Short:   "List backups for a database",
	Aliases: []string{"ls"},
	Args:    cobra.ExactArgs(1),
	RunE:    runDBBackupList,
}

var dbBackupDeleteCmd = &cobra.Command{
	Use:   "delete <name> <backup>",
	Short: "Delete a single backup (the recovery-window anchor is protected)",
	Args:  cobra.ExactArgs(2),
	RunE:  runDBBackupDelete,
}

func runDBBackupList(cmd *cobra.Command, args []string) error {
	c := getClient()
	id, err := resolveDatabaseID(c, args[0])
	if err != nil {
		return err
	}
	backups, err := c.ListBackups(context.Background(), id)
	if err != nil {
		return err
	}

	rows := make([][]string, len(backups))
	for i, b := range backups {
		rows[i] = []string{b.Name, b.Type, b.Method, renderStatus(b.Status), b.StartedAt, b.StoppedAt}
	}
	render([]string{"NAME", "TYPE", "METHOD", "STATUS", "STARTED", "STOPPED"}, rows, backups)
	return nil
}

func runDBBackupDelete(cmd *cobra.Command, args []string) error {
	c := getClient()
	id, err := resolveDatabaseID(c, args[0])
	if err != nil {
		return err
	}
	if err := c.DeleteBackup(context.Background(), id, args[1]); err != nil {
		return err
	}
	fmt.Println(renderInfoBox("Backup Deleted", [][]string{{"Backup", args[1]}}))
	return nil
}

func runDBBackupCreate(cmd *cobra.Command, args []string) error {
	c := getClient()
	id, err := resolveDatabaseID(c, args[0])
	if err != nil {
		return err
	}
	outputFormat := rootCmd.Flag("output").Value.String()

	// --external: back up directly to the configured external (BYOB) bucket via
	// a keyless pg_dump Job, rather than the default managed store.
	if external, _ := cmd.Flags().GetBool("external"); external {
		var run *client.BackupDestinationRun
		var runErr error
		action := func() { run, runErr = c.RunBackupDestination(context.Background(), id) }
		if !isStructured(outputFormat) {
			withSpinner("Starting external backup...", action)
		} else {
			action()
		}
		if runErr != nil {
			return runErr
		}
		if isStructured(outputFormat) {
			return renderData(run)
		}
		fmt.Println(renderInfoBox("External Backup Started", [][]string{
			{"Job", run.JobName},
			{"Status", renderStatus(run.Status)},
			{"Identity", run.Subject},
		}))
		return nil
	}

	var backup *client.DatabaseBackup
	var backupErr error
	action := func() {
		backup, backupErr = c.CreateBackup(context.Background(), id)
	}

	if !isStructured(outputFormat) {
		withSpinner("Starting backup...", action)
	} else {
		action()
	}
	if backupErr != nil {
		return backupErr
	}

	if isStructured(outputFormat) {
		return renderData(backup)
	}

	fmt.Println(renderInfoBox("Backup Started", [][]string{
		{"Name", backup.Name},
		{"Type", backup.Type},
		{"Status", renderStatus(backup.Status)},
	}))
	return nil
}

// dbBackupDestCmd groups the external (BYOB) backup-destination commands.
var dbBackupDestCmd = &cobra.Command{
	Use:     "destination",
	Aliases: []string{"dest"},
	Short:   "Manage a database's external (bring-your-own-bucket) backup destination",
}

var dbBackupDestSetCmd = &cobra.Command{
	Use:   "set <name>",
	Short: "Configure the external backup destination (your own S3/GCS bucket, keyless)",
	Args:  cobra.ExactArgs(1),
	RunE:  runDBBackupDestSet,
}

var dbBackupDestShowCmd = &cobra.Command{
	Use:     "show <name>",
	Aliases: []string{"get"},
	Short:   "Show the external backup destination + last run",
	Args:    cobra.ExactArgs(1),
	RunE:    runDBBackupDestShow,
}

var dbBackupDestUnsetCmd = &cobra.Command{
	Use:   "unset <name>",
	Short: "Remove the external backup destination",
	Args:  cobra.ExactArgs(1),
	RunE:  runDBBackupDestUnset,
}

func runDBBackupDestSet(cmd *cobra.Command, args []string) error {
	c := getClient()
	id, err := resolveDatabaseID(c, args[0])
	if err != nil {
		return err
	}
	dest := client.BackupDestination{
		Provider:        flagStr(cmd, "provider"),
		Bucket:          flagStr(cmd, "bucket"),
		Region:          flagStr(cmd, "region"),
		Prefix:          flagStr(cmd, "prefix"),
		FlatLayout:      flagBool(cmd, "flat-layout"),
		RoleARN:         flagStr(cmd, "role-arn"),
		WIFProvider:     flagStr(cmd, "wif-provider"),
		ServiceAccount:  flagStr(cmd, "service-account"),
		Endpoint:        flagStr(cmd, "endpoint"),
		AccessKeyID:     flagStr(cmd, "access-key-id"),
		SecretAccessKey: flagStr(cmd, "secret-access-key"),
		Schedule:        flagStr(cmd, "schedule"),
	}
	out, err := c.SetBackupDestination(context.Background(), id, dest)
	if err != nil {
		return err
	}
	if isStructured(rootCmd.Flag("output").Value.String()) {
		return renderData(out)
	}
	fmt.Println(renderInfoBox("Backup Destination Set", backupDestRows(out)))
	return nil
}

func runDBBackupDestShow(cmd *cobra.Command, args []string) error {
	c := getClient()
	id, err := resolveDatabaseID(c, args[0])
	if err != nil {
		return err
	}
	dest, err := c.GetBackupDestination(context.Background(), id)
	if err != nil {
		return err
	}
	if isStructured(rootCmd.Flag("output").Value.String()) {
		return renderData(dest)
	}
	fmt.Println(renderInfoBox("Backup Destination", backupDestRows(dest)))
	return nil
}

func runDBBackupDestUnset(cmd *cobra.Command, args []string) error {
	c := getClient()
	id, err := resolveDatabaseID(c, args[0])
	if err != nil {
		return err
	}
	if err := c.DeleteBackupDestination(context.Background(), id); err != nil {
		return err
	}
	fmt.Println(successBox.Render("✓ Backup destination removed."))
	return nil
}

func backupDestRows(d *client.BackupDestination) [][]string {
	rows := [][]string{
		{"Provider", d.Provider},
		{"Bucket", d.Bucket},
	}
	if d.Region != "" {
		rows = append(rows, []string{"Region", d.Region})
	}
	if d.Prefix != "" {
		rows = append(rows, []string{"Prefix", d.Prefix})
	}
	if d.FlatLayout {
		rows = append(rows, []string{"Flat Layout", "yes (no project/database nesting)"})
	}
	if d.RoleARN != "" {
		rows = append(rows, []string{"Role ARN", d.RoleARN})
	}
	if d.WIFProvider != "" {
		rows = append(rows, []string{"WIF Provider", d.WIFProvider})
		rows = append(rows, []string{"Service Account", d.ServiceAccount})
	}
	if d.Endpoint != "" {
		rows = append(rows, []string{"Endpoint", d.Endpoint})
	}
	if d.AccessKeyID != "" {
		rows = append(rows, []string{"Access Key ID", d.AccessKeyID})
	}
	schedule := d.Schedule
	if schedule == "" {
		schedule = "on-demand only"
	}
	rows = append(rows, []string{"Schedule", schedule})
	if d.LastRunStatus != "" {
		rows = append(rows, []string{"Last Run", d.LastRunStatus + " (" + d.LastRunAt + ")"})
	}
	return rows
}

var dbBackupConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage backup configuration",
}

var dbBackupConfigShowCmd = &cobra.Command{
	Use:     "show <name>",
	Aliases: []string{"get"},
	Short:   "Show backup configuration",
	Args:    cobra.ExactArgs(1),
	RunE:    runDBBackupConfigShow,
}

var dbBackupConfigSetCmd = &cobra.Command{
	Use:   "set <name>",
	Short: "Update backup configuration",
	Args:  cobra.ExactArgs(1),
	RunE:  runDBBackupConfigSet,
}

func runDBBackupConfigShow(cmd *cobra.Command, args []string) error {
	c := getClient()
	id, err := resolveDatabaseID(c, args[0])
	if err != nil {
		return err
	}
	config, err := c.GetBackupConfig(context.Background(), id)
	if err != nil {
		return err
	}

	outputFormat := rootCmd.Flag("output").Value.String()
	if isStructured(outputFormat) {
		return renderData(config)
	}

	enabledStr := mutedStyle.Render("disabled")
	if config.Enabled {
		enabledStr = lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("enabled")
	}

	schedule := config.Schedule
	if schedule == "" {
		schedule = mutedStyle.Render("not set")
	}

	recoverability := config.FirstRecoverabilityPoint
	if recoverability == "" {
		recoverability = mutedStyle.Render("unknown")
	}

	rows := [][]string{
		{"Enabled", enabledStr},
		{"Schedule", schedule},
		{"Retention", config.Retention},
		{"Recoverable from", recoverability},
	}
	health := lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("producing backups")
	if len(config.Problems) > 0 {
		health = lipgloss.NewStyle().Bold(true).Foreground(colorDanger).Render(fmt.Sprintf("%d problem(s)", len(config.Problems)))
	}
	rows = append(rows, []string{"Health", health})
	fmt.Println(renderInfoBox("Backup Configuration", rows))

	for _, p := range config.Problems {
		line := fmt.Sprintf("%s: %s", p.Reason, p.Detail)
		if p.Since != "" {
			line += " (since " + p.Since + ")"
		}
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(colorDanger).Render("  ! ") + line)
	}
	return nil
}

func runDBBackupConfigSet(cmd *cobra.Command, args []string) error {
	schedule, _ := cmd.Flags().GetString("schedule")
	retention, _ := cmd.Flags().GetString("retention")
	disabled, _ := cmd.Flags().GetBool("disable")

	c := getClient()
	id, err := resolveDatabaseID(c, args[0])
	if err != nil {
		return err
	}
	config := client.BackupConfig{
		Enabled:   !disabled,
		Schedule:  schedule,
		Retention: retention,
	}

	if err := c.UpdateBackupConfig(context.Background(), id, config); err != nil {
		return err
	}

	fmt.Println(successBox.Render(
		lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
			" Backup configuration updated.",
	))
	return nil
}

var dbRestoreCmd = &cobra.Command{
	Use:   "restore <name>",
	Short: "Restore a database from backup",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target, _ := cmd.Flags().GetString("target")
		pit, _ := cmd.Flags().GetString("point-in-time")
		external, _ := cmd.Flags().GetBool("external")

		c := getClient()
		id, err := resolveDatabaseID(c, args[0])
		if err != nil {
			return err
		}
		outputFormat := rootCmd.Flag("output").Value.String()

		// --external: restore in place from a dump in the configured external
		// (BYOB) bucket (keyless pg_restore Job). --from names the dump (default:
		// the latest backup).
		if external {
			object, _ := cmd.Flags().GetString("from")
			var run *client.BackupDestinationRun
			var runErr error
			action := func() { run, runErr = c.RestoreBackupDestination(context.Background(), id, object) }
			if !isStructured(outputFormat) {
				withSpinner("Starting external restore...", action)
			} else {
				action()
			}
			if runErr != nil {
				return runErr
			}
			if isStructured(outputFormat) {
				return renderData(run)
			}
			fmt.Println(renderInfoBox("External Restore Started", [][]string{
				{"Job", run.JobName},
				{"Status", renderStatus(run.Status)},
				{"Identity", run.Subject},
			}))
			return nil
		}

		if target == "" {
			return fmt.Errorf("--target is required")
		}

		var restored *client.Database
		var restoreErr error
		action := func() {
			restored, restoreErr = c.RestoreDatabase(context.Background(), id, client.RestoreRequest{
				PointInTime: pit,
				TargetName:  target,
			})
		}

		if !isStructured(outputFormat) {
			withSpinner("Restoring database...", action)
		} else {
			action()
		}
		if restoreErr != nil {
			return restoreErr
		}

		if isStructured(outputFormat) {
			return renderData(restored)
		}

		fmt.Println(renderInfoBox("Database Restored", [][]string{
			{"ID", mutedStyle.Render(restored.ID)},
			{"Name", restored.Name},
			{"Status", renderStatus(restored.Status)},
		}))
		return nil
	},
}

var dbUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update a running database (cpu, memory, instances, storage, version, pooler)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveDatabaseID(c, args[0])
		if err != nil {
			return err
		}
		existing, err := c.GetDatabase(context.Background(), id)
		if err != nil {
			return err
		}

		req := client.UpdateDatabaseRequest{}
		req.DisplayName, _ = cmd.Flags().GetString("display-name")
		req.CPU, _ = cmd.Flags().GetString("cpu")
		req.Memory, _ = cmd.Flags().GetString("memory")
		req.Storage, _ = cmd.Flags().GetString("storage")
		req.Version, _ = cmd.Flags().GetString("postgres-version")
		if cmd.Flags().Changed("instances") {
			n, _ := cmd.Flags().GetInt64("instances")
			req.Instances = &n
		}
		if cmd.Flags().Changed("pooler") {
			p, _ := cmd.Flags().GetBool("pooler")
			req.Pooler = &p
		}
		if req.CPU == "" && req.Memory == "" && req.Storage == "" && req.Version == "" &&
			req.Instances == nil && req.Pooler == nil {
			return fmt.Errorf("nothing to update: set at least one of --cpu --memory --instances --storage --postgres-version --pooler")
		}

		db, err := c.UpdateDatabase(context.Background(), existing.ID, req)
		if err != nil {
			return err
		}

		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(db)
		}
		fmt.Println(renderInfoBox("Database Updated", [][]string{
			{"ID", mutedStyle.Render(db.ID)},
			{"Name", db.Name},
			{"Display Name", db.DisplayName},
			{"Engine", db.Engine},
			{"Version", db.Version},
			{"Status", renderStatus(db.Status)},
		}))
		fmt.Println(mutedStyle.Render("CNPG is reconciling the change; instance/version changes roll the pods."))
		return nil
	},
}

func addBackupExternalFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("external", false, "Back up to the configured external (BYOB) bucket instead of the managed store")
}

func addBackupConfigSetFlags(cmd *cobra.Command) {
	cmd.Flags().String("schedule", "0 3 * * *", "5-field cron schedule for automated backups (minute hour day-of-month month day-of-week)")
	cmd.Flags().String("retention", "30d", "Backup retention period")
	cmd.Flags().Bool("disable", false, "Disable scheduled backups")
}

func addBackupDestSetFlags(cmd *cobra.Command) {
	cmd.Flags().String("provider", "aws", "Backup provider: aws | gcp | s3 (generic S3-compatible: R2, B2, Hetzner, Garage)")
	cmd.Flags().String("bucket", "", "Destination bucket name (required)")
	cmd.Flags().String("region", "", "Bucket region (aws; s3 optional, e.g. 'auto' for R2)")
	cmd.Flags().String("prefix", "", "Optional key prefix within the bucket")
	cmd.Flags().Bool("flat-layout", false, "Skip the <project>/<database> nesting fpcloud adds after prefix (bucket root when prefix is also empty)")
	cmd.Flags().String("role-arn", "", "AWS IAM role ARN assumed via web identity (aws)")
	cmd.Flags().String("wif-provider", "", "GCP workload-identity provider resource (gcp)")
	cmd.Flags().String("service-account", "", "GCP target service-account email (gcp)")
	cmd.Flags().String("endpoint", "", "S3 endpoint URL, e.g. https://<acct>.r2.cloudflarestorage.com (s3)")
	cmd.Flags().String("access-key-id", "", "S3 static access key id (s3)")
	cmd.Flags().String("secret-access-key", "", "S3 static secret access key; write-only, omit on update to keep (s3)")
	cmd.Flags().String("schedule", "", "Cron schedule for automatic backups; empty = on-demand only")
}

func init() {
	dbCreateCmd.Flags().String("version", "17", "Database version")
	dbCreateCmd.Flags().String("display-name", "", "Cosmetic label (defaults to the name)")
	dbCreateCmd.Flags().String("cpu", "", "CPU request/limit per instance (e.g. 500m); default 250m")
	dbCreateCmd.Flags().String("memory", "", "Memory request/limit per instance (e.g. 2Gi); default 1Gi")
	dbCreateCmd.Flags().String("storage", "", "Persistent storage size (e.g. 20Gi); default 10Gi")
	dbCreateCmd.Flags().Bool("pooler", false, "Enable a PgBouncer connection pooler (adds DATABASE_POOL_URL)")

	dbUpdateCmd.Flags().String("display-name", "", "New cosmetic label")
	dbUpdateCmd.Flags().String("cpu", "", "New CPU request/limit per instance (e.g. 500m)")
	dbUpdateCmd.Flags().String("memory", "", "New memory request/limit per instance (e.g. 1Gi)")
	dbUpdateCmd.Flags().String("storage", "", "New storage size (grow only, e.g. 20Gi)")
	dbUpdateCmd.Flags().Int64("instances", 1, "Number of Postgres instances (HA)")
	dbUpdateCmd.Flags().String("postgres-version", "", "New Postgres major version (forward only)")
	dbUpdateCmd.Flags().Bool("pooler", false, "Enable/disable the PgBouncer pooler")

	dbDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	dbRestoreCmd.Flags().String("target", "", "Name for the restored database")
	dbRestoreCmd.Flags().String("point-in-time", "", "Point-in-time to restore to (RFC3339)")
	dbRestoreCmd.Flags().Bool("external", false, "Restore in place from the external (BYOB) bucket")
	dbRestoreCmd.Flags().String("from", "", "External backup object to restore (default: latest)")

	addBackupExternalFlag(dbBackupCreateCmd)
	addBackupConfigSetFlags(dbBackupConfigSetCmd)
	addBackupDestSetFlags(dbBackupDestSetCmd)

	dbBackupConfigCmd.AddCommand(dbBackupConfigShowCmd, dbBackupConfigSetCmd)
	dbBackupDestCmd.AddCommand(dbBackupDestSetCmd, dbBackupDestShowCmd, dbBackupDestUnsetCmd)
	dbBackupCmd.AddCommand(dbBackupCreateCmd, dbBackupListCmd, dbBackupDeleteCmd,
		dbBackupConfigCmd, dbBackupDestCmd)

	dbCmd.AddCommand(dbCreateCmd, dbListCmd, dbGetCmd, dbUpdateCmd, dbDeleteCmd,
		dbBackupCmd, dbRestoreCmd)
	rootCmd.AddCommand(dbCmd)
}

// flagStr returns a string flag's value (empty when unset).
func flagStr(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

// flagBool returns a bool flag's value (false when unset).
func flagBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}

// dbAddress renders a database's cluster-internal address. It is not reachable
// from a workstation — `db connect` tunnels to it — but naming it is how an
// operator or an in-cluster consumer wires anything up by hand.
func dbAddress(db *client.Database) string {
	if db.Host == "" {
		return ""
	}
	if db.Port == 0 {
		return db.Host
	}
	return fmt.Sprintf("%s:%d", db.Host, db.Port)
}

// orDash renders an empty value as a muted dash.
func orDash(v string) string {
	if v == "" {
		return mutedStyle.Render("—")
	}
	return v
}
