package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/qubered/beacon/internal/core/store"
)

// runMigrateCommand implements `beaconctl migrate`.
//
// Migration is a separate operator action rather than something Core does on
// startup. A process that reshapes the database it happens to find will one
// day be an old binary rolled back onto a newer schema, quietly migrating it
// somewhere nobody intended.
func runMigrateCommand(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	dsn := fs.String("database-url", os.Getenv("BEACON_DATABASE_URL"), "PostgreSQL connection URL (or $BEACON_DATABASE_URL)")
	dir := fs.String("dir", "migrations", "directory holding the numbered SQL migrations")
	dryRun := fs.Bool("dry-run", false, "list what would be applied, and apply nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dsn == "" {
		return fmt.Errorf("--database-url is required (or set BEACON_DATABASE_URL)")
	}

	migrations, err := store.LoadMigrations(os.DirFS("."), *dir)
	if err != nil {
		return err
	}
	if len(migrations) == 0 {
		return fmt.Errorf("no migrations found in %s", *dir)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	st, err := store.Open(ctx, *dsn)
	if err != nil {
		return err
	}
	defer st.Close()

	if *dryRun {
		for _, m := range migrations {
			fmt.Printf("  %04d %s\n", m.Version, m.Name)
		}
		fmt.Printf("\n%d migration(s) on disk; --dry-run applied none\n", len(migrations))
		return nil
	}

	applied, err := st.Migrate(ctx, migrations)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		fmt.Println("database is up to date")
		return nil
	}
	for _, v := range applied {
		fmt.Printf("applied %04d\n", v)
	}
	return nil
}
