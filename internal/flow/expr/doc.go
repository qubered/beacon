// Package expr is the tier-1 expression language: restricted, non-Turing-complete, guaranteed to terminate.
//
// Spec §6.5. Arithmetic, comparison, logical and membership operators, field and index access, and all/exists/map/filter comprehension macros.
//
// Two things to get right. Domain functions (hex, BCD, CRC, raw-to-dBm, tick-to-duration, byte extraction at offset, integer decode by width and endianness) keep users off tier 2. And guaranteed termination is not bounded cost: comprehensions over a large SNMP walk are polynomial in input size, which AST-size caps do not bound, so cost is budgeted against input size and collection sizes are capped explicitly.
//
// Invariant I4: secret() resolves to a sealed frame handle. An expression can never read a secret value.
package expr
