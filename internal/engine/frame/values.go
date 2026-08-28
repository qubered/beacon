package frame

import "fmt"

// Status is the value carried by a status-typed frame (types.Status()).
//
// Degraded is a state in its own right, not a shade of down — the UI colours it
// distinctly and alerting routes it separately (spec §15).
type Status string

const (
	StatusUnknown  Status = "unknown"
	StatusUp       Status = "up"
	StatusDegraded Status = "degraded"
	StatusDown     Status = "down"
)

func (s Status) Valid() bool {
	switch s {
	case StatusUnknown, StatusUp, StatusDegraded, StatusDown:
		return true
	}
	return false
}

// ErrorClass classifies a failure. The class is what an alert policy routes on
// (spec §11), so the distinctions matter more than they look.
//
// The important one is ClassProtocol: it means the device answered and we
// misread it, which is a flow bug or a firmware change rather than a gear fault.
// After a firmware update, twelve monitors going protocol at once is a
// completely different message from twelve going timeout, and they should reach
// different people.
type ErrorClass string

const (
	ClassNone             ErrorClass = "none"
	ClassTimeout          ErrorClass = "timeout"
	ClassConnectRefused   ErrorClass = "connect_refused"
	ClassDNS              ErrorClass = "dns"
	ClassTLS              ErrorClass = "tls"
	ClassAuth             ErrorClass = "auth"
	ClassProtocol         ErrorClass = "protocol"
	ClassAssertion        ErrorClass = "assertion"
	ClassSandboxTimeout   ErrorClass = "sandbox_timeout"
	ClassSandboxMemory    ErrorClass = "sandbox_memory"
	ClassAgentUnreachable ErrorClass = "agent_unreachable"
	ClassInternal         ErrorClass = "internal"
)

// Failure is the value carried by an error-typed frame (types.Error()).
//
// It is a value rather than a plain Go error because it travels down an error
// port as a frame, and a downstream node needs to branch on its class — which is
// how "if the API is unreachable, fall back to ping" is expressed (spec §6.2).
type Failure struct {
	Class   ErrorClass `json:"class"`
	Node    string     `json:"node,omitempty"`
	Message string     `json:"message"`
}

func (f Failure) Error() string {
	if f.Node != "" {
		return fmt.Sprintf("%s: %s (%s)", f.Node, f.Message, f.Class)
	}
	return fmt.Sprintf("%s (%s)", f.Message, f.Class)
}

// Fail builds a Failure. Prefer it over constructing the struct so the class is
// always a deliberate choice rather than the zero value.
func Fail(class ErrorClass, format string, args ...any) Failure {
	return Failure{Class: class, Message: fmt.Sprintf(format, args...)}
}

// Value is the value carried by a record-typed frame (types.Record()): named
// fields, as produced by regex extract, parse and table select.
type Record = map[string]any
