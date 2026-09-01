package egress

import (
	"context"
	"errors"
)

// ErrNoPolicy is returned when a transport node runs without an egress policy
// in its context.
//
// This fails closed on purpose. A transport that found no policy and dialled
// anyway would have unrestricted network access from every VLAN an agent sits
// in, which is precisely the outcome invariant I7 exists to prevent — and it
// would arrive silently, as a wiring mistake nobody notices until an audit.
var ErrNoPolicy = errors.New("no egress policy in context: a transport node cannot open a connection without one")

type dialerKey struct{}

// WithDialer attaches the agent's dialer to a run's context. The agent
// executor calls this once per run; nodes only ever read it.
//
// Context is the channel for this because of the two boundaries in
// docs/architecture.md: the engine does not know what a node does, so it
// cannot hand a dialer to the transports specifically, and nodes do not know
// where they are running, so a transport cannot reach for the agent's dialer
// itself. The context carries the deadline the same way and for the same
// reason.
func WithDialer(ctx context.Context, d *Dialer) context.Context {
	return context.WithValue(ctx, dialerKey{}, d)
}

// DialerFrom retrieves the dialer, or ErrNoPolicy if there is none.
func DialerFrom(ctx context.Context) (*Dialer, error) {
	d, ok := ctx.Value(dialerKey{}).(*Dialer)
	if !ok || d == nil {
		return nil, ErrNoPolicy
	}
	return d, nil
}
