// Package acceptance is the harness for the five acceptance scenarios in spec §18.
//
// These are the design's falsification tests, not a feature list. Each must be buildable entirely through the UI with no code changes; the harness asserts that each scenario's flow is composed only of catalogue nodes and produces the expected status against test/devsim. If one cannot be built, the node catalogue has a gap — and the fix is a node, never a vendor adapter.
package acceptance
