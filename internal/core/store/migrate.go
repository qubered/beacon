package store

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Migration is one numbered SQL file.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// LoadMigrations reads and orders the numbered SQL files in dir.
//
// Ordering is by the leading number, parsed as an integer rather than compared
// as a string — lexical ordering would run 0010 before 0009 the moment the
// count reaches double digits, and the symptom would be a migration failing
// against a schema that had not been built yet.
func LoadMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("reading migrations directory: %w", err)
	}

	var out []Migration
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		numPart, rest, found := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
		if !found {
			return nil, fmt.Errorf("migration %q is not named <number>_<name>.sql", name)
		}
		version, err := strconv.Atoi(numPart)
		if err != nil {
			return nil, fmt.Errorf("migration %q does not start with a number: %w", name, err)
		}
		body, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("reading migration %q: %w", name, err)
		}
		out = append(out, Migration{Version: version, Name: rest, SQL: string(body)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i := 1; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, fmt.Errorf("two migrations share version %d (%s and %s)", out[i].Version, out[i-1].Name, out[i].Name)
		}
	}
	return out, nil
}

// Migrate applies every migration not yet recorded as applied.
//
// Each runs inside its own transaction together with the row recording it, so
// a migration that fails leaves neither a half-applied schema nor a claim that
// it succeeded. Forward-only: there are no down migrations, because a
// down migration that has never been run in anger is a rollback plan nobody
// has tested.
func (s *Store) Migrate(ctx context.Context, migrations []Migration) (applied []int, err error) {
	if _, err := s.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    int PRIMARY KEY,
    name       text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
)`); err != nil {
		return nil, fmt.Errorf("creating the migration ledger: %w", err)
	}

	done := map[int]bool{}
	rows, err := s.pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("reading applied migrations: %w", err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		done[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, m := range migrations {
		if done[m.Version] {
			continue
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return applied, fmt.Errorf("beginning migration %04d_%s: %w", m.Version, m.Name, err)
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return applied, fmt.Errorf("applying migration %04d_%s: %w", m.Version, m.Name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, m.Version, m.Name); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return applied, fmt.Errorf("recording migration %04d_%s: %w", m.Version, m.Name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return applied, fmt.Errorf("committing migration %04d_%s: %w", m.Version, m.Name, err)
		}
		applied = append(applied, m.Version)
	}
	return applied, nil
}
