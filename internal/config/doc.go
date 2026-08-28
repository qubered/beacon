// Package config loads and validates runtime configuration for both Core and Agent.
//
// Owns the bound ceilings in spec §6.2 (run wall clock, node budget, loop cap, branch concurrency, frame size, capture size, sandbox memory/CPU). Defaults are suggestions; ceilings are enforced and an operator cannot configure past them.
package config
