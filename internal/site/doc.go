// Package site carries and enforces site scoping.
//
// Decision D2 keeps the product single-site; decision D30 (docs/decisions/0002) enforces the scope from the first migration anyway. Every store query and API route takes a site.ID. Nothing here implements tenancy UI, billing or cross-site routing.
package site
