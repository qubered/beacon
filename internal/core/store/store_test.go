package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qubered/beacon/internal/site"
)

// uniq makes every name a test inserts distinct, including across repeated
// runs against the same database. Site names are unique by schema, and a test
// that only passes on a freshly created database is a test people stop running.
var seq atomic.Int64

func uniq(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), seq.Add(1))
}

// testStore connects to the database named by BEACON_TEST_DATABASE_URL,
// migrates it, and returns a store scoped to a fresh site.
//
// Skipping when the variable is unset keeps `go test ./...` working on a
// laptop with no database, while CI always sets it — so these run on every
// push even though they are skippable locally.
func testStore(t *testing.T) (*Store, site.ID, context.Context) {
	t.Helper()
	dsn := os.Getenv("BEACON_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BEACON_TEST_DATABASE_URL is not set; skipping the database-backed tests")
	}

	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(s.Close)

	migrations, err := LoadMigrations(os.DirFS("../../.."), "migrations")
	if err != nil {
		t.Fatalf("loading migrations: %v", err)
	}
	if _, err := s.Migrate(ctx, migrations); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	// A fresh site per test, so tests cannot see each other's rows — which is
	// also a live check that site scoping actually scopes.
	var id string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO sites (name, timezone) VALUES ($1, 'UTC') RETURNING id`,
		uniq("site"),
	).Scan(&id); err != nil {
		t.Fatalf("creating a test site: %v", err)
	}
	return s, site.ID(id), ctx
}

// seedMonitor creates the device, flow, flow version and monitor rows a run
// needs to satisfy its foreign keys, and returns the monitor and flow version.
func seedMonitor(t *testing.T, s *Store, ctx context.Context, siteID site.ID) (monitorID, flowVersionID string) {
	t.Helper()
	var agentID, deviceID, flowID string

	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	must(s.pool.QueryRow(ctx,
		`INSERT INTO agents (site_id, name) VALUES ($1,$2) RETURNING id`,
		string(siteID), uniq("agent")).Scan(&agentID), "creating an agent")
	must(s.pool.QueryRow(ctx,
		`INSERT INTO devices (site_id, agent_id, name, host) VALUES ($1,$2,$3,$4) RETURNING id`,
		string(siteID), agentID, uniq("device"), "10.0.0.5").Scan(&deviceID), "creating a device")
	must(s.pool.QueryRow(ctx,
		`INSERT INTO flows (site_id, name) VALUES ($1,$2) RETURNING id`,
		string(siteID), uniq("flow")).Scan(&flowID), "creating a flow")
	must(s.pool.QueryRow(ctx,
		`INSERT INTO flow_versions (site_id, flow_id, version, graph, graph_schema_version, content_hash, published_at)
		 VALUES ($1,$2,1,'{}'::jsonb,1,$3, now()) RETURNING id`,
		string(siteID), flowID, uniq("hash")).Scan(&flowVersionID), "creating a flow version")
	must(s.pool.QueryRow(ctx,
		`INSERT INTO monitors (site_id, device_id, flow_id, flow_version_id, name, interval_ms, timeout_ms)
		 VALUES ($1,$2,$3,$4,$5,60000,30000) RETURNING id`,
		string(siteID), deviceID, flowID, flowVersionID, uniq("monitor")).Scan(&monitorID), "creating a monitor")

	return monitorID, flowVersionID
}

func sampleRun(monitorID, flowVersionID string, slot time.Time) Run {
	return Run{
		MonitorID:     monitorID,
		ScheduledAt:   slot,
		StartedAt:     slot.Add(20 * time.Millisecond),
		Duration:      150 * time.Millisecond,
		FlowVersionID: flowVersionID,
		Status:        StateUp,
		Outcome:       OutcomeOK,
	}
}

// TestInsertRun_ScheduledSlotIsAnExecutionFence is the property that makes
// at-least-once spool delivery safe: a replayed result must deduplicate on
// insert rather than double-count uptime (spec §7.3).
func TestInsertRun_ScheduledSlotIsAnExecutionFence(t *testing.T) {
	s, siteID, ctx := testStore(t)
	monitorID, fv := seedMonitor(t, s, ctx, siteID)
	slot := time.Now().UTC().Truncate(time.Second)

	inserted, err := s.InsertRun(ctx, siteID, sampleRun(monitorID, fv, slot))
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if !inserted {
		t.Fatal("the first insert of a slot reported no row written")
	}

	// The spool reconnects and re-sends the same result, as at-least-once
	// delivery is entitled to do.
	inserted, err = s.InsertRun(ctx, siteID, sampleRun(monitorID, fv, slot))
	if err != nil {
		t.Fatalf("replay must not be an error — it is the normal recovery path: %v", err)
	}
	if inserted {
		t.Fatal("a replayed result was inserted a second time; the fence is not holding")
	}

	runs, err := s.RecentRuns(ctx, siteID, monitorID, 10)
	if err != nil {
		t.Fatalf("reading runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("%d rows stored for one slot, want 1", len(runs))
	}
}

// TestInsertRun_RequiresAFlowVersion: without it a historical run cannot be
// explained once the flow is edited, so the capture would be read against a
// graph that did not produce it.
func TestInsertRun_RequiresAFlowVersion(t *testing.T) {
	s, siteID, ctx := testStore(t)
	monitorID, _ := seedMonitor(t, s, ctx, siteID)

	r := sampleRun(monitorID, "", time.Now().UTC())
	if _, err := s.InsertRun(ctx, siteID, r); err == nil {
		t.Fatal("a run with no flow version was accepted")
	}
}

func TestInsertRun_RequiresASite(t *testing.T) {
	s, siteID, ctx := testStore(t)
	monitorID, fv := seedMonitor(t, s, ctx, siteID)

	if _, err := s.InsertRun(ctx, "", sampleRun(monitorID, fv, time.Now().UTC())); err != site.ErrNoSite {
		t.Fatalf("err = %v, want site.ErrNoSite", err)
	}
}

// TestRecentRuns_IsScopedToItsSite is D30 checked against a live database
// rather than trusted from the code shape.
func TestRecentRuns_IsScopedToItsSite(t *testing.T) {
	s, siteID, ctx := testStore(t)
	monitorID, fv := seedMonitor(t, s, ctx, siteID)

	if _, err := s.InsertRun(ctx, siteID, sampleRun(monitorID, fv, time.Now().UTC())); err != nil {
		t.Fatal(err)
	}

	var otherSite string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO sites (name, timezone) VALUES ($1,'UTC') RETURNING id`, uniq("other-site")).Scan(&otherSite); err != nil {
		t.Fatal(err)
	}

	runs, err := s.RecentRuns(ctx, site.ID(otherSite), monitorID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("a query scoped to another site returned %d rows", len(runs))
	}
}

func TestInsertRun_RoundTripsItsFields(t *testing.T) {
	s, siteID, ctx := testStore(t)
	monitorID, fv := seedMonitor(t, s, ctx, siteID)
	slot := time.Now().UTC().Truncate(time.Millisecond)

	want := Run{
		MonitorID:        monitorID,
		ScheduledAt:      slot,
		StartedAt:        slot.Add(time.Second),
		Duration:         1234 * time.Millisecond,
		FlowVersionID:    fv,
		Attempt:          2,
		Status:           StateDown,
		Outcome:          OutcomeFailed,
		ErrorClass:       "timeout",
		Message:          "device did not answer",
		IsTransition:     true,
		Capture:          json.RawMessage(`{"node":"ping"}`),
		CaptureTruncated: true,
	}
	if _, err := s.InsertRun(ctx, siteID, want); err != nil {
		t.Fatal(err)
	}

	got, err := s.RecentRuns(ctx, siteID, monitorID, 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("reading back: %v (%d rows)", err, len(got))
	}
	g := got[0]
	if g.Status != want.Status || g.Outcome != want.Outcome || g.ErrorClass != want.ErrorClass {
		t.Errorf("status/outcome/class = %s/%s/%s, want %s/%s/%s", g.Status, g.Outcome, g.ErrorClass, want.Status, want.Outcome, want.ErrorClass)
	}
	if g.Message != want.Message || !g.IsTransition || g.Attempt != 2 {
		t.Errorf("message/transition/attempt = %q/%v/%d", g.Message, g.IsTransition, g.Attempt)
	}
	if g.Duration != want.Duration {
		t.Errorf("duration = %s, want %s", g.Duration, want.Duration)
	}
	// jsonb normalises: it reformats whitespace and does not preserve key
	// order, so a capture never round-trips byte-for-byte. Compare the decoded
	// value instead — and note the consequence for M6's byte inspector, which
	// must not assume the JSON it reads back is textually what was written.
	if !g.CaptureTruncated {
		t.Error("capture_truncated did not round-trip")
	}
	var gotCap, wantCap map[string]any
	if err := json.Unmarshal(g.Capture, &gotCap); err != nil {
		t.Fatalf("stored capture is not valid JSON: %v", err)
	}
	if err := json.Unmarshal(want.Capture, &wantCap); err != nil {
		t.Fatal(err)
	}
	if len(gotCap) != len(wantCap) || gotCap["node"] != wantCap["node"] {
		t.Errorf("capture = %v, want %v", gotCap, wantCap)
	}
}

// TestRecordStateChange_ClosesTheOpenPeriod: the two halves must happen
// together, or uptime double-counts an interval or loses one.
func TestRecordStateChange_ClosesTheOpenPeriod(t *testing.T) {
	s, siteID, ctx := testStore(t)
	monitorID, _ := seedMonitor(t, s, ctx, siteID)
	t0 := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)

	if err := s.RecordStateChange(ctx, siteID, monitorID, StateUp, t0, false); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordStateChange(ctx, siteID, monitorID, StateDown, t0.Add(18*time.Hour), false); err != nil {
		t.Fatal(err)
	}

	periods, err := s.StatePeriods(ctx, siteID, monitorID, t0, t0.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(periods) != 2 {
		t.Fatalf("%d periods, want 2", len(periods))
	}
	if periods[0].State != StateUp || periods[0].To.IsZero() {
		t.Errorf("the first period was not closed: %+v", periods[0])
	}
	if !periods[0].To.Equal(periods[1].From) {
		t.Errorf("periods do not abut: %s ends at %s but %s starts at %s — uptime would lose or double-count that gap",
			periods[0].State, periods[0].To, periods[1].State, periods[1].From)
	}
	if !periods[1].To.IsZero() {
		t.Error("the newest period should still be open")
	}
}

// TestRecordStateChange_IgnoresARepeatedState: alerting re-asserts the current
// state routinely, and a row per re-assertion would bloat the table uptime
// scans without changing any number it produces.
func TestRecordStateChange_IgnoresARepeatedState(t *testing.T) {
	s, siteID, ctx := testStore(t)
	monitorID, _ := seedMonitor(t, s, ctx, siteID)
	t0 := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	for i := 0; i < 5; i++ {
		if err := s.RecordStateChange(ctx, siteID, monitorID, StateUp, t0.Add(time.Duration(i)*time.Minute), false); err != nil {
			t.Fatal(err)
		}
	}
	periods, err := s.StatePeriods(ctx, siteID, monitorID, t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(periods) != 1 {
		t.Fatalf("%d periods after five identical assertions, want 1", len(periods))
	}
}

// TestUptime_ReadsThroughToTheSameArithmetic wires the SQL to ComputeUptime,
// so the exhaustively-tested pure function is provably the one in the path.
func TestUptime_ReadsThroughToTheSameArithmetic(t *testing.T) {
	s, siteID, ctx := testStore(t)
	monitorID, _ := seedMonitor(t, s, ctx, siteID)

	day := time.Now().UTC().Truncate(time.Hour).Add(-24 * time.Hour)
	if err := s.RecordStateChange(ctx, siteID, monitorID, StateUp, day, false); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordStateChange(ctx, siteID, monitorID, StateDown, day.Add(18*time.Hour), false); err != nil {
		t.Fatal(err)
	}

	u, err := s.Uptime(ctx, siteID, monitorID, day, day.Add(24*time.Hour), day.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !u.HasData {
		t.Fatal("HasData is false")
	}
	if want := 0.75; u.Raw < want-0.001 || u.Raw > want+0.001 {
		t.Errorf("raw uptime = %v, want ~%v", u.Raw, want)
	}
}

// TestCountMissedRuns_RecordsAGapWithoutInventingRuns. The runs did not
// happen; fabricating rows for them would put invented observations into the
// history uptime is computed from.
func TestCountMissedRuns_RecordsAGapWithoutInventingRuns(t *testing.T) {
	s, siteID, ctx := testStore(t)
	monitorID, _ := seedMonitor(t, s, ctx, siteID)

	if err := s.CountMissedRuns(ctx, siteID, monitorID, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.CountThrottledRun(ctx, siteID, monitorID); err != nil {
		t.Fatal(err)
	}

	var missed, throttled int
	if err := s.pool.QueryRow(ctx,
		`SELECT missed_runs, throttled_runs FROM monitors WHERE site_id=$1 AND id=$2`,
		string(siteID), monitorID).Scan(&missed, &throttled); err != nil {
		t.Fatal(err)
	}
	if missed != 10 || throttled != 1 {
		t.Errorf("missed=%d throttled=%d, want 10 and 1", missed, throttled)
	}

	runs, err := s.RecentRuns(ctx, siteID, monitorID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("%d run rows were invented for missed slots; a gap is a counter, not fabricated history", len(runs))
	}
}

func TestMigrate_IsIdempotent(t *testing.T) {
	s, _, ctx := testStore(t)
	migrations, err := LoadMigrations(os.DirFS("../../.."), "migrations")
	if err != nil {
		t.Fatal(err)
	}
	applied, err := s.Migrate(ctx, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 0 {
		t.Errorf("re-running migrations applied %v; it must be a no-op", applied)
	}
}

// TestLoadMigrations_OrdersNumerically: lexical ordering runs 0010 before 0009
// the moment the count reaches double digits, and the symptom is a migration
// failing against a schema that was never built.
func TestLoadMigrations_OrdersNumerically(t *testing.T) {
	ms, err := LoadMigrations(os.DirFS("../../.."), "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(ms); i++ {
		if ms[i].Version <= ms[i-1].Version {
			t.Fatalf("migrations are not in ascending order: %d then %d", ms[i-1].Version, ms[i].Version)
		}
	}
}
