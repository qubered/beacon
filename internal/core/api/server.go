package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/qubered/beacon/internal/core/store"
	"github.com/qubered/beacon/internal/site"
)

// Reader is the read-only slice of the store this API needs.
//
// An interface rather than *store.Store so the handlers can be tested without
// a database, and so the read-only surface is visible in the type: a handler
// here cannot write even by accident, because it has nothing to write with.
type Reader interface {
	ListDevices(ctx context.Context, siteID site.ID) ([]store.DeviceSummary, error)
	ListMonitorStatus(ctx context.Context, siteID site.ID) ([]store.MonitorStatus, error)
	Uptime(ctx context.Context, siteID site.ID, monitorID string, from, to, now time.Time) (store.Uptime, error)
}

// Server is the Core HTTP API.
//
// M3 ships the read-only half — the status wall and the device list. Writes,
// authentication and the audit log arrive with the rest of §16 in later
// milestones; until then this is deliberately incapable of changing anything.
type Server struct {
	Reader Reader
	Log    *slog.Logger

	// Site is the single site this server serves. Decision D2 keeps the
	// product single-site while D30 carries the scope anyway, so it is passed
	// explicitly into every query rather than being implicit.
	Site site.ID
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/devices", s.handleDevices)
	mux.HandleFunc("GET /api/v1/monitors", s.handleMonitors)
	mux.HandleFunc("GET /api/v1/monitors/{id}/uptime", s.handleUptime)
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	return mux
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := s.Reader.ListDevices(r.Context(), s.Site)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": orEmpty(devices)})
}

func (s *Server) handleMonitors(w http.ResponseWriter, r *http.Request) {
	monitors, err := s.Reader.ListMonitorStatus(r.Context(), s.Site)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"monitors": orEmpty(monitors)})
}

// handleUptime serves a monitor's uptime over a window.
//
// The window is explicit in the query string rather than defaulted silently,
// because "uptime" without a stated period is a number nobody can check. It
// also carries the deep-link contract from spec §12 — a Grafana panel links
// here with the range that showed the dip.
func (s *Server) handleUptime(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	now := time.Now().UTC()

	to, err := timeParam(r, "to", now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	from, err := timeParam(r, "from", to.Add(-24*time.Hour))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !to.After(from) {
		writeError(w, http.StatusBadRequest, "the 'to' time must be after 'from'")
		return
	}

	u, err := s.Reader.Uptime(r.Context(), s.Site, id, from, to, now)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	// Both figures, always, and never one collapsed into the other: a single
	// number that quietly excludes maintenance is what makes uptime reporting
	// untrustworthy (spec §10). has_data distinguishes "no idea" from "down
	// all day", which a bare 0.0 cannot.
	writeJSON(w, http.StatusOK, map[string]any{
		"monitor_id":            id,
		"from":                  from,
		"to":                    to,
		"has_data":              u.HasData,
		"raw":                   u.Raw,
		"excluding_maintenance": u.ExcludingMaintenance,
		"total_seconds":         u.Total.Seconds(),
		"up_seconds":            u.Up.Seconds(),
		"maintenance_seconds":   u.Maintenance.Seconds(),
		"unknown_seconds":       u.Unknown.Seconds(),
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func timeParam(r *http.Request, name string, fallback time.Time) (time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be an RFC3339 timestamp", name)
	}
	return t.UTC(), nil
}

// fail logs the real error and returns a generic one.
//
// A database error message can carry a query, a column name or a value, and
// this API will eventually be reachable by people who should not see the
// schema. The operator gets the detail in the log; the client gets a status.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, site.ErrNoSite) {
		writeError(w, http.StatusInternalServerError, "the server has no site configured")
		return
	}
	if s.Log != nil {
		s.Log.Error("api request failed", "path", r.URL.Path, "error", err)
	}
	writeError(w, http.StatusInternalServerError, "the request could not be completed")
}

// orEmpty guarantees a JSON array rather than null.
//
// The API's contract must not depend on whether a reader happened to return a
// nil slice. A null makes every consumer write a null check, and the one that
// forgets crashes on an empty site — which is exactly the site someone is
// looking at on their first day.
func orEmpty[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
