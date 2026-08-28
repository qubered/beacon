// Package parse holds the parse and extract nodes.
//
// Spec §6.4: Parse, Regex Extract, JSONPath/Pointer, XPath, Table Select, Split Text, Coerce.
//
// Regex Extract must use a non-backtracking engine — users supply the patterns (spec §16) — and the same engine must power the editor's live match preview, or the preview will disagree with the runtime on exactly the patterns that matter. Go's stdlib regexp is RE2 and satisfies both; the editor calls it over the API rather than running a second engine in the browser.
//
// Coerce is never implicit: an explicit format and an on-failure branch.
package parse
