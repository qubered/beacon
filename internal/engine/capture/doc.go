// Package capture records per-node input and output for every execution, and masks sealed values.
//
// Principle 5: every run is inspectable. "Why did this go red?" must be answerable by clicking the red node and reading the actual bytes that came back.
//
// Spec §8 retention: the last N runs per monitor, plus every run in a suspect window — from the first failure through the confirming transition, because the diagnostic capture is almost always the first failure, not the one that officially changed state. Captures are dropped at write time when retention says so, never written and pruned later.
package capture
