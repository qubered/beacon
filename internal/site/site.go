package site

import "errors"

// ID identifies a site. It is carried on every entity, every store query and
// every API route.
//
// Decision D2 keeps the product single-site; decision D30 enforces the scope
// anyway, because §19.7 of the spec is right that the cost of enforcing it now
// is far below the cost of retrofitting it later. Nothing here implements
// tenancy — no UI, no routing, no billing. It is a scoping discipline.
type ID string

// ErrNoSite is returned when a scoped operation is attempted without a site.
// Store methods take an ID explicitly rather than reading it from a context, so
// forgetting to scope a query is a compile error rather than a data leak.
var ErrNoSite = errors.New("operation requires a site scope")

func (s ID) Valid() bool { return s != "" }
