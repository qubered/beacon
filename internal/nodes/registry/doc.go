// Package registry is the node type catalogue: descriptors, port signatures, config schemas and schema versions.
//
// An agent's declared capability set (spec §7.5) is generated from this registry, so capability gating is a property of the build rather than a hand-maintained list. Core refuses to assign a monitor whose flow needs a node type or config schema version the target agent does not implement, and surfaces it in the UI rather than failing at 6pm.
package registry
