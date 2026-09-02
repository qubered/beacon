package store

import (
	"context"
	"fmt"
	"time"

	"github.com/qubered/beacon/internal/site"
)

// RecordStateChange closes the monitor's open period and opens a new one.
//
// Periods are the source of truth for uptime (D23), so the two halves must
// happen together: a crash between them would either double-count an interval
// or leave a hole, and both corrupt every uptime figure computed afterwards.
// A transaction is the whole reason this is one method rather than two.
//
// A change to the state the monitor is already in is ignored rather than
// producing a zero-length period. Alerting re-asserts the current state
// routinely, and a row per re-assertion would bloat the table that uptime
// scans without changing a single number it produces.
func (s *Store) RecordStateChange(ctx context.Context, siteID site.ID, monitorID string, newState State, at time.Time, inMaintenance bool) error {
	if !siteID.Valid() {
		return site.ErrNoSite
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning state change for monitor %s: %w", monitorID, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	var currentState State
	var currentMaint bool
	var currentFrom time.Time
	err = tx.QueryRow(ctx, `
SELECT state, in_maintenance, from_at FROM monitor_state_periods
WHERE site_id = $1 AND monitor_id = $2 AND to_at IS NULL
ORDER BY from_at DESC LIMIT 1`, string(siteID), monitorID).Scan(&currentState, &currentMaint, &currentFrom)

	switch {
	case err == nil:
		if currentState == newState && currentMaint == inMaintenance {
			return nil // nothing changed; do not write a zero-length period
		}
		if !at.After(currentFrom) {
			// Out-of-order arrival. Backfill replays history in order, so this
			// means clock skew or a bug rather than a normal case; refusing is
			// better than silently writing a period that ends before it began.
			return fmt.Errorf("state change for monitor %s at %s is not after the open period's start %s", monitorID, at, currentFrom)
		}
		if _, err := tx.Exec(ctx, `
UPDATE monitor_state_periods SET to_at = $4
WHERE site_id = $1 AND monitor_id = $2 AND from_at = $3`,
			string(siteID), monitorID, currentFrom, at); err != nil {
			return fmt.Errorf("closing the open period for monitor %s: %w", monitorID, err)
		}
	case mapNoRows(err) == ErrNotFound:
		// No open period: this is the monitor's first observation.
	default:
		return fmt.Errorf("reading the open period for monitor %s: %w", monitorID, err)
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO monitor_state_periods (site_id, monitor_id, state, from_at, in_maintenance)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (monitor_id, from_at) DO NOTHING`,
		string(siteID), monitorID, string(newState), at, inMaintenance); err != nil {
		return fmt.Errorf("opening a %s period for monitor %s: %w", newState, monitorID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing state change for monitor %s: %w", monitorID, err)
	}
	return nil
}

// StatePeriods returns the periods overlapping [from, to), which is what
// ComputeUptime consumes.
//
// The overlap test deliberately includes periods that started before the
// window and periods still open, so "uptime for yesterday" is answerable from
// a period that began last week and has not ended.
func (s *Store) StatePeriods(ctx context.Context, siteID site.ID, monitorID string, from, to time.Time) ([]StatePeriod, error) {
	if !siteID.Valid() {
		return nil, site.ErrNoSite
	}

	const q = `
SELECT state, from_at, to_at, in_maintenance
FROM monitor_state_periods
WHERE site_id = $1 AND monitor_id = $2
  AND from_at < $4
  AND (to_at IS NULL OR to_at > $3)
ORDER BY from_at`

	rows, err := s.pool.Query(ctx, q, string(siteID), monitorID, from, to)
	if err != nil {
		return nil, fmt.Errorf("querying state periods for monitor %s: %w", monitorID, err)
	}
	defer rows.Close()

	var out []StatePeriod
	for rows.Next() {
		var p StatePeriod
		var to *time.Time
		if err := rows.Scan(&p.State, &p.From, &to, &p.InMaintenance); err != nil {
			return nil, fmt.Errorf("scanning state period: %w", err)
		}
		if to != nil {
			p.To = *to
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Uptime computes a monitor's uptime over a window, both raw and excluding
// maintenance (spec §10).
//
// The arithmetic is deliberately in Go rather than in SQL. Clipping to the
// window, open periods and maintenance overlapping a state change are where
// the subtleties live, and having them in a pure function means they can be
// tested exhaustively without a database — see ComputeUptime.
func (s *Store) Uptime(ctx context.Context, siteID site.ID, monitorID string, from, to, now time.Time) (Uptime, error) {
	periods, err := s.StatePeriods(ctx, siteID, monitorID, from, to)
	if err != nil {
		return Uptime{}, err
	}
	return ComputeUptime(periods, from, to, now), nil
}
