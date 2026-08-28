// Package pack is the Pack bundle format: manifest schema, read/write, and signature verification.
//
// Exported so third parties can author and validate Packs without importing Beacon internals.
//
// A Pack carries flows, node presets, recorded device fixtures, suggested monitor bindings, device-matching profiles, dashboards and default alert rules, plus an author, a signature, a minimum platform version, a declared egress scope, declared required agent capabilities, the names (never values) of credentials it needs, and whether it contains write-capable nodes.
//
// Decision D27: fixtures are tests. They let someone author a Pack at a desk with the gear locked in a venue, they are the Pack's regression tests in CI, and when a firmware update changes a response format the fixture is what tells you which of the twelve flows broke.
package pack
