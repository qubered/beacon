// Package packs installs, exports, verifies and forks Packs.
//
// Spec §14. Installation shows a review diff: what flows, what node types, what egress scope, which credentials it will request, and whether write-capable nodes are involved. The installer grants at most the declared egress scope. Packs never carry secret values.
//
// Fork-friendly: installing a Pack and then editing a flow forks it into the local library and marks it detached, with a merge prompt when the Pack updates. AV techs will always need to tweak something.
package packs
