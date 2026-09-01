// Package transport implements the transport primitives. Every one of them emits bytes (invariant I1).
//
// Spec §6.4: TCP Request, Send/Expect, UDP, HTTP, GraphQL, WebSocket, SNMP, ICMP Ping, TCP Connect, DNS Query, CIDR Sweep, SSH Exec, MQTT, Modbus TCP, TLS Inspect, and the HTTP and SNMP trap listeners.
//
// M2 ships the tier-1 set: TCP Request, TCP Connect, HTTP Request, DNS Query, TLS Inspect and ICMP Ping. The rest arrive with the catalogue in M8, and Send/Expect with Connection Scope in M7.
//
// Every transport reaches the network through internal/agent/egress's Dialer, which it reads from its context — never net.Dial directly. That is what makes the egress check unskippable rather than something each new transport has to remember, and a transport that finds no policy in its context fails closed. Anything with a client that resolves on its own behalf (as net/http does) must pin inside its own dial, not before the call.
//
// Principle 2: primitives are honest. A TCP node opens a TCP socket. It does not helpfully trim whitespace, assume UTF-8 or normalise line endings unless told to.
//
// Every transport needs an explicit abort adapter that destroys the socket or closes the client when the run deadline fires — not every I/O API accepts a cancellation signal, and a deadline that stops at the API boundary does not satisfy I2.
//
// Deliberate omission (D8): there is no shell/exec node. It turns a monitoring server into a remote code execution surface for anyone with flow-author rights.
package transport
