package store

import (
	"context"
	"fmt"
	"time"

	"github.com/qubered/beacon/internal/site"
)

// DeviceSummary is one row of the device list.
type DeviceSummary struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Host   string   `json:"host"`
	Tags   []string `json:"tags"`
	Health string   `json:"health"`

	// Reachability is separate from Health on purpose: a device can be
	// reachable and unhealthy (it answered, and a reading was out of range),
	// and collapsing the two loses exactly the distinction an operator needs
	// to know whether to walk to the rack.
	Reachability string `json:"reachability"`

	AgentID      string     `json:"agent_id"`
	MonitorCount int        `json:"monitor_count"`
	HealthSince  *time.Time `json:"health_since,omitempty"`
}

// ListDevices returns the device inventory for a site.
func (s *Store) ListDevices(ctx context.Context, siteID site.ID) ([]DeviceSummary, error) {
	if !siteID.Valid() {
		return nil, site.ErrNoSite
	}

	const q = `
SELECT d.id, d.name, d.host, d.tags, d.health, d.reachability, d.agent_id,
       d.health_changed_at,
       (SELECT count(*) FROM monitors m WHERE m.site_id = d.site_id AND m.device_id = d.id)
FROM devices d
WHERE d.site_id = $1
ORDER BY d.name`

	rows, err := s.pool.Query(ctx, q, string(siteID))
	if err != nil {
		return nil, fmt.Errorf("listing devices: %w", err)
	}
	defer rows.Close()

	out := []DeviceSummary{}
	for rows.Next() {
		var d DeviceSummary
		var since *time.Time
		if err := rows.Scan(&d.ID, &d.Name, &d.Host, &d.Tags, &d.Health, &d.Reachability,
			&d.AgentID, &since, &d.MonitorCount); err != nil {
			return nil, fmt.Errorf("scanning device: %w", err)
		}
		d.HealthSince = since
		out = append(out, d)
	}
	return out, rows.Err()
}

// MonitorStatus is one tile on the status wall.
type MonitorStatus struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`

	State      string     `json:"state"`
	StateSince *time.Time `json:"state_since,omitempty"`
	Enabled    bool       `json:"enabled"`

	// Flapping is surfaced as a first-class number rather than buried
	// (spec §11): a monitor oscillating is a different problem from one that
	// is down, and an operator who cannot see the difference chases the wrong
	// fault.
	FlapPercent float32 `json:"flap_percent"`
	IsFlapping  bool    `json:"is_flapping"`

	IntervalMS int `json:"interval_ms"`

	// MissedRuns and ThrottledRuns are shown because a non-zero value means
	// the platform stopped collecting, or someone over-scheduled a device —
	// both of which look like a healthy monitor if you only read the state.
	MissedRuns    int64 `json:"missed_runs"`
	ThrottledRuns int64 `json:"throttled_runs"`

	// LastRunAt is what distinguishes "the check failed" from "the platform
	// stopped checking" (spec §12). Without it a stale green tile is
	// indistinguishable from a fresh one.
	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	LastStatus *string    `json:"last_status,omitempty"`
	ErrorClass *string    `json:"error_class,omitempty"`
	Message    *string    `json:"message,omitempty"`
}

// ListMonitorStatus returns the status wall for a site.
//
// One query with a lateral join rather than a query per monitor: at a thousand
// monitors the per-monitor version is a thousand round trips every time
// somebody opens the page, and the page is the one people leave open on a
// screen in the rack room.
func (s *Store) ListMonitorStatus(ctx context.Context, siteID site.ID) ([]MonitorStatus, error) {
	if !siteID.Valid() {
		return nil, site.ErrNoSite
	}

	const q = `
SELECT m.id, m.name, m.device_id, d.name,
       m.state, m.state_since, m.enabled,
       m.flap_percent, m.is_flapping, m.interval_ms,
       m.missed_runs, m.throttled_runs,
       r.scheduled_at, r.status::text, r.error_class::text, r.message
FROM monitors m
JOIN devices d ON d.id = m.device_id AND d.site_id = m.site_id
LEFT JOIN LATERAL (
    SELECT scheduled_at, status, error_class, message
    FROM monitor_runs mr
    WHERE mr.site_id = m.site_id AND mr.monitor_id = m.id
    ORDER BY mr.scheduled_at DESC
    LIMIT 1
) r ON true
WHERE m.site_id = $1
ORDER BY d.name, m.name`

	rows, err := s.pool.Query(ctx, q, string(siteID))
	if err != nil {
		return nil, fmt.Errorf("listing monitor status: %w", err)
	}
	defer rows.Close()

	out := []MonitorStatus{}
	for rows.Next() {
		var m MonitorStatus
		if err := rows.Scan(&m.ID, &m.Name, &m.DeviceID, &m.DeviceName,
			&m.State, &m.StateSince, &m.Enabled,
			&m.FlapPercent, &m.IsFlapping, &m.IntervalMS,
			&m.MissedRuns, &m.ThrottledRuns,
			&m.LastRunAt, &m.LastStatus, &m.ErrorClass, &m.Message); err != nil {
			return nil, fmt.Errorf("scanning monitor status: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
