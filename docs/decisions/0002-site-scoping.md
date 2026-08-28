# D30 — Site scoping is enforced from the first migration

**Status:** accepted
**Extends:** D2. **Answers:** spec §19.7

## Decision

`site_id` is not merely carried. Every table has it, every store method takes a
`site.ID` as an explicit parameter, and every API route resolves one before
touching the store.

No tenancy features ship. No tenancy UI, no cross-site routing, no per-site
billing, no isolation guarantees beyond the scoping itself. The product is
single-site (D2) and says so.

## Why

§19.7 asks the question and effectively answers it: the cost of enforcing this
now is far below the cost of retrofitting it later. Retrofitting means auditing
every query in the system for a missing predicate, which is exactly the class of
work that gets 95% done.

Taking the scope as an explicit parameter rather than reading it from a context
value makes forgetting to scope a query a compile error rather than a data leak.

## Cost, accepted

Slightly more ceremony on every store call, and one parameter that is the same
value everywhere until a second site exists.
