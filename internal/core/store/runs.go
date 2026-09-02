package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/qubered/beacon/internal/site"
)

// Outcome is how a run ended, distinct from the status it reported. It mirrors
// the run_outcome enum in migration 0002.
//
// Throttled is its own outcome and not a failure: the device never
// misbehaved, the schedule did (spec §8).
type Outcome string

const (
	OutcomeOK        Outcome = "ok"
	OutcomeFailed    Outcome = "failed"
	OutcomeThrottled Outcome = "throttled"
	OutcomeSkipped   Outcome = "skipped"
	OutcomeError     Outcome = "error"
)

// Run is one execution of a monitor's flow.
type Run struct {
	MonitorID string

	// ScheduledAt is the slot, not the execution time. It is the execution
	// fence: at-least-once spool delivery replays results, and this is what
	// makes a replay deduplicate on insert rather than double-count uptime
	// (spec §7.3).
	ScheduledAt time.Time
	StartedAt   time.Time
	Duration    time.Duration

	// FlowVersionID is required. Without it you cannot explain a historical
	// run once the flow has been edited — the capture would be interpreted
	// against a graph that did not produce it.
	FlowVersionID string
	Attempt       int

	Status     State
	Outcome    Outcome
	ErrorClass string
	Message    string

	// IsTransition is a stored fact rather than alerting-layer logic, so alert
	// history is queryable and incidents have a natural anchor (spec §11).
	IsTransition bool

	// Capture is nil when retention says this run gets none. Captures are
	// dropped at write time, never written and pruned later (spec §8) — a
	// capture written and deleted an hour later has already cost the disk
	// write, the WAL, the replication and the vacuum.
	Capture          json.RawMessage
	CaptureTruncated bool
}

// InsertRun records a run, ignoring a replay of one already stored.
//
// Returns inserted=false when the fence rejected it. That is the normal,
// expected path for an at-least-once spool that reconnected and re-sent, not
// an error — treating it as one would turn ordinary recovery into a stream of
// alerts.
func (s *Store) InsertRun(ctx context.Context, siteID site.ID, r Run) (inserted bool, err error) {
	if !siteID.Valid() {
		return false, site.ErrNoSite
	}
	if r.FlowVersionID == "" {
		return false, fmt.Errorf("run for monitor %s has no flow version: a run that cannot be explained later is not worth storing", r.MonitorID)
	}
	if r.ErrorClass == "" {
		r.ErrorClass = "none"
	}

	const q = `
INSERT INTO monitor_runs (
    site_id, monitor_id, scheduled_at, started_at, duration_ms,
    flow_version_id, attempt, status, outcome, error_class, message,
    is_transition, capture, capture_truncated
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (monitor_id, scheduled_at) DO NOTHING`

	tag, err := s.pool.Exec(ctx, q,
		string(siteID), r.MonitorID, r.ScheduledAt, r.StartedAt, r.Duration.Milliseconds(),
		r.FlowVersionID, r.Attempt, string(r.Status), string(r.Outcome), r.ErrorClass, nullString(r.Message),
		r.IsTransition, nullJSON(r.Capture), r.CaptureTruncated,
	)
	if err != nil {
		return false, fmt.Errorf("inserting run for monitor %s at %s: %w", r.MonitorID, r.ScheduledAt, err)
	}
	return tag.RowsAffected() == 1, nil
}

// RecentRuns returns a monitor's most recent runs, newest first.
func (s *Store) RecentRuns(ctx context.Context, siteID site.ID, monitorID string, limit int) ([]Run, error) {
	if !siteID.Valid() {
		return nil, site.ErrNoSite
	}
	if limit <= 0 {
		limit = 50
	}

	const q = `
SELECT monitor_id, scheduled_at, started_at, duration_ms, flow_version_id, attempt,
       status, outcome, error_class, coalesce(message, ''), is_transition,
       capture, capture_truncated
FROM monitor_runs
WHERE site_id = $1 AND monitor_id = $2
ORDER BY scheduled_at DESC
LIMIT $3`

	rows, err := s.pool.Query(ctx, q, string(siteID), monitorID, limit)
	if err != nil {
		return nil, fmt.Errorf("querying runs for monitor %s: %w", monitorID, err)
	}
	defer rows.Close()

	var out []Run
	for rows.Next() {
		var r Run
		var ms int64
		var capture []byte
		if err := rows.Scan(
			&r.MonitorID, &r.ScheduledAt, &r.StartedAt, &ms, &r.FlowVersionID, &r.Attempt,
			&r.Status, &r.Outcome, &r.ErrorClass, &r.Message, &r.IsTransition,
			&capture, &r.CaptureTruncated,
		); err != nil {
			return nil, fmt.Errorf("scanning run: %w", err)
		}
		r.Duration = time.Duration(ms) * time.Millisecond
		if capture != nil {
			r.Capture = json.RawMessage(capture)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountMissedRuns adds to a monitor's missed-run counter, for slots the
// scheduler snapped past.
//
// A gap is recorded as a counter rather than as run rows on purpose: the runs
// did not happen, and inventing rows to describe them would put fabricated
// observations into the history that uptime is computed from.
func (s *Store) CountMissedRuns(ctx context.Context, siteID site.ID, monitorID string, missed int) error {
	if !siteID.Valid() {
		return site.ErrNoSite
	}
	if missed <= 0 {
		return nil
	}
	const q = `UPDATE monitors SET missed_runs = missed_runs + $3 WHERE site_id = $1 AND id = $2`
	if _, err := s.pool.Exec(ctx, q, string(siteID), monitorID, missed); err != nil {
		return fmt.Errorf("recording %d missed runs for monitor %s: %w", missed, monitorID, err)
	}
	return nil
}

// CountThrottledRun increments the throttled counter. A non-zero value means
// someone has over-scheduled a device and needs to know (spec §8).
func (s *Store) CountThrottledRun(ctx context.Context, siteID site.ID, monitorID string) error {
	if !siteID.Valid() {
		return site.ErrNoSite
	}
	const q = `UPDATE monitors SET throttled_runs = throttled_runs + 1 WHERE site_id = $1 AND id = $2`
	if _, err := s.pool.Exec(ctx, q, string(siteID), monitorID); err != nil {
		return fmt.Errorf("recording a throttled run for monitor %s: %w", monitorID, err)
	}
	return nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullJSON(j json.RawMessage) any {
	if len(j) == 0 {
		return nil
	}
	return []byte(j)
}

// errNoRows lets callers distinguish "not found" without importing pgx.
var ErrNotFound = errors.New("not found")

func mapNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
