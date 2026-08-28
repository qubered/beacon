// Package transform holds the transform nodes.
//
// Spec §6.4: Scale & Offset, Lookup Table, Unit Convert, Delta/Rate, Aggregate, Merge/Set/Pick/Omit, Expression, and the sandboxed Transform.
//
// Scale & Offset exists as a node specifically so that converting a raw RF value to dBm requires zero expression syntax. Lookup Table exists so a large vendor fault enumeration is editable configuration in a Pack rather than a switch statement needing a platform release. Delta/Rate handles 32- and 64-bit counter wrap and returns null on the first run.
package transform
