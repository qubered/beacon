// Package metrics serves Prometheus exposition and owns cardinality enforcement.
//
// Spec §12, three tiers that must not be conflated: agent-local, Core process-local, and Core aggregate. The aggregate view is served by exactly one elected process — with N processes each querying the database for all monitors, every sum and every rate is N times wrong.
//
// Ship the last-run timestamp. It is the one people forget, and it is what lets a dashboard express "the platform stopped checking" as distinct from "the check failed".
//
// Escaping rules differ between help text and label values and a device name containing a quote can break the entire scrape, not one line — so escape centrally, in one function, with a test. Duplicate name-plus-label-set combinations reject the whole page, and with user-authored flows this will happen: deduplicate before rendering and emit a warning metric instead.
package metrics
