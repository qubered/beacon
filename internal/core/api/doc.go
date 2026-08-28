// Package api is the Core HTTP API: REST for configuration, streaming for live test runs and status.
//
// Every route is site-scoped (internal/site) and role-gated (internal/core/auth). Everything an author or admin does is audit-logged with a before/after diff.
//
// Owns the deep-link contract from spec §12: a monitor URL accepting a time range, so a Grafana panel can link back into the run capture that explains a dip.
package api
