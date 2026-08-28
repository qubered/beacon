# D31 — AGPL-3.0

**Status:** accepted
**Answers:** spec §19.8

## Decision

The platform is licensed AGPL-3.0. See [LICENSE](../../LICENSE).

## Why

The three requirements from §19.8 are: self-hosting must be unencumbered,
community Packs must be possible, and a commercial future must not be foreclosed.
AGPL-3.0 satisfies all three. It is OSI-approved, so Pack authors and outside
contributors are not chilled the way a source-available licence chills them, and
the network clause means a hosted competitor cannot take the work closed.

## Consequences to handle

- **Packs are content, not derivative works.** The Pack format is deliberately
  in `pkg/pack` rather than `internal/`, and a Pack is data consumed by the
  platform. Pack authors choose their own licence. State this in the Pack index
  documentation so nobody assumes authoring a Pack makes their flows AGPL.
- **A CLA is required before accepting outside contributions** if relicensing
  should ever remain possible. Decide this before the first external PR, not
  after.
