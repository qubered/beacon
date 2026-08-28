// Package secrets implements envelope encryption at rest and the sealed-frame plumbing.
//
// Invariant I4. A secret is never readable by a user, never written to a run capture, never exported in a Pack, and never reachable from an expression or the sandbox.
//
// Sealing is a property of a Frame, not of a field name: anything derived from a sealed frame stays sealed until it passes through a declared one-way function (hash, HMAC). Capture masking is by value scan, so a secret composed into a larger payload is masked in the hex dump too (spec §16).
//
// The honest limitation is documented in docs/security.md and must stay there: sealed frames stop accidental disclosure, not a determined author.
package secrets
