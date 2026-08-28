// Package emit holds the evaluate and emit nodes.
//
// Spec §6.4: Assert, Threshold, Emit Status, Emit Metric, Emit Event, Set State, Read State, Emit Device Candidate.
//
// Assert covers roughly 80% of real checks with no expression at all (decision D9); it is why most users never need the expression layer. Emit Metric accepts label values only from a declared label schema — free text is rejected at authoring time (invariant I11, decision D26). Set State has an explicit target — run, shared or subscription — so the destination is never ambiguous.
//
// A run that never reaches an Emit Status produces unknown plus an execution warning.
package emit
