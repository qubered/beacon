// Package graph is the flow DAG model: nodes, typed ports, edges, and immutable published versions.
//
// Invariant I3 and decision D28: a published flow version is immutable and editing forks a draft. Versions are content-addressable so agent-side caching is trivially correct.
//
// The only legal cycle in the graph is the back-edge into a Loop node, which carries a mandatory iteration cap.
package graph
