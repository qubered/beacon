package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qubered/beacon/internal/core/store"
	"github.com/qubered/beacon/internal/site"
)

type fakeReader struct {
	devices  []store.DeviceSummary
	monitors []store.MonitorStatus
	uptime   store.Uptime
	err      error

	gotSite site.ID
	gotFrom time.Time
	gotTo   time.Time
}

func (f *fakeReader) ListDevices(ctx context.Context, s site.ID) ([]store.DeviceSummary, error) {
	f.gotSite = s
	return f.devices, f.err
}

func (f *fakeReader) ListMonitorStatus(ctx context.Context, s site.ID) ([]store.MonitorStatus, error) {
	f.gotSite = s
	return f.monitors, f.err
}

func (f *fakeReader) Uptime(ctx context.Context, s site.ID, id string, from, to, now time.Time) (store.Uptime, error) {
	f.gotSite, f.gotFrom, f.gotTo = s, from, to
	return f.uptime, f.err
}

func serve(t *testing.T, r Reader, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	s := &Server{Reader: r, Site: "site-1"}
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, rec.Body.String())
	}
	return body
}

func TestDevices_ReturnsTheInventory(t *testing.T) {
	r := &fakeReader{devices: []store.DeviceSummary{
		{ID: "d1", Name: "projector-1", Host: "10.0.0.5", Health: "up", Reachability: "reachable", MonitorCount: 3},
	}}
	rec := serve(t, r, "GET", "/api/v1/devices")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if r.gotSite != "site-1" {
		t.Errorf("query was scoped to %q, want site-1", r.gotSite)
	}
	devices := decode(t, rec)["devices"].([]any)
	if len(devices) != 1 {
		t.Fatalf("%d devices", len(devices))
	}
}

// TestDevices_EmptyIsAnArrayNotNull: a null here makes every consumer write a
// null check, and the one that forgets crashes on an empty site.
func TestDevices_EmptyIsAnArrayNotNull(t *testing.T) {
	rec := serve(t, &fakeReader{}, "GET", "/api/v1/devices")
	if got := rec.Body.String(); !strings.Contains(got, `"devices":[]`) {
		t.Fatalf("empty response is %s, want an empty array", got)
	}
}

func TestMonitors_ReturnsTheStatusWall(t *testing.T) {
	now := time.Now().UTC()
	r := &fakeReader{monitors: []store.MonitorStatus{
		{ID: "m1", Name: "power", DeviceName: "projector-1", State: "up", LastRunAt: &now},
	}}
	rec := serve(t, r, "GET", "/api/v1/monitors")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	monitors := decode(t, rec)["monitors"].([]any)
	if len(monitors) != 1 {
		t.Fatalf("%d monitors", len(monitors))
	}
	m := monitors[0].(map[string]any)
	// last_run_at is what distinguishes "the check failed" from "the platform
	// stopped checking" (spec §12).
	if _, ok := m["last_run_at"]; !ok {
		t.Error("last_run_at is absent; a stale tile would be indistinguishable from a fresh one")
	}
}

// TestUptime_ReportsBothFiguresAndWhetherItHasData: a single number that
// quietly excludes maintenance is what makes uptime reporting untrustworthy.
func TestUptime_ReportsBothFiguresAndWhetherItHasData(t *testing.T) {
	r := &fakeReader{uptime: store.Uptime{
		Raw: 0.75, ExcludingMaintenance: 1.0, HasData: true,
		Total: 24 * time.Hour, Up: 18 * time.Hour, Maintenance: 6 * time.Hour,
	}}
	rec := serve(t, r, "GET", "/api/v1/monitors/m1/uptime")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	body := decode(t, rec)
	if body["raw"] != 0.75 {
		t.Errorf("raw = %v, want 0.75", body["raw"])
	}
	if body["excluding_maintenance"] != 1.0 {
		t.Errorf("excluding_maintenance = %v, want 1.0", body["excluding_maintenance"])
	}
	if body["has_data"] != true {
		t.Error("has_data is missing or false")
	}
}

// TestUptime_DefaultsToTheLastDayButAcceptsARange is the deep-link contract
// from spec §12: a Grafana panel links here with the range that showed the dip.
func TestUptime_DefaultsToTheLastDayButAcceptsARange(t *testing.T) {
	r := &fakeReader{}
	serve(t, r, "GET", "/api/v1/monitors/m1/uptime")
	if d := r.gotTo.Sub(r.gotFrom); d < 23*time.Hour || d > 25*time.Hour {
		t.Errorf("default window is %s, want about 24h", d)
	}

	r = &fakeReader{}
	serve(t, r, "GET", "/api/v1/monitors/m1/uptime?from=2026-03-01T00:00:00Z&to=2026-03-02T00:00:00Z")
	if !r.gotFrom.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("from = %s, want the requested time", r.gotFrom)
	}
	if d := r.gotTo.Sub(r.gotFrom); d != 24*time.Hour {
		t.Errorf("window = %s, want exactly 24h", d)
	}
}

func TestUptime_RejectsABadRange(t *testing.T) {
	cases := []string{
		"/api/v1/monitors/m1/uptime?from=yesterday",
		"/api/v1/monitors/m1/uptime?from=2026-03-02T00:00:00Z&to=2026-03-01T00:00:00Z",
	}
	for _, path := range cases {
		rec := serve(t, &fakeReader{}, "GET", path)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s returned %d, want 400", path, rec.Code)
		}
	}
}

// TestErrors_DoNotLeakTheSchema: a database error can carry a query, a column
// name or a value, and this API will eventually be reachable by people who
// should not see the schema.
func TestErrors_DoNotLeakTheSchema(t *testing.T) {
	r := &fakeReader{err: errors.New(`ERROR: column "secret_column" does not exist in monitor_runs`)}
	rec := serve(t, r, "GET", "/api/v1/devices")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret_column") {
		t.Fatalf("the response leaked the underlying error: %s", rec.Body)
	}
}

// TestAPI_IsReadOnly. M3 ships the read-only half deliberately; a write route
// appearing before authentication and the audit log exist would be a hole.
func TestAPI_IsReadOnly(t *testing.T) {
	s := &Server{Reader: &fakeReader{}, Site: "site-1"}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		rec := httptest.NewRecorder()
		s.Routes().ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/devices", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /api/v1/devices returned %d, want 405 — the M3 API must not accept writes", method, rec.Code)
		}
	}
}
