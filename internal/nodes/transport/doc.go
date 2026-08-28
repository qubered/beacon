// Package transport implements the transport primitives. Every one of them emits bytes (invariant I1).
//
// Spec §6.4: TCP Request, Send/Expect, UDP, HTTP, GraphQL, WebSocket, SNMP, ICMP Ping, TCP Connect, DNS Query, CIDR Sweep, SSH Exec, MQTT, Modbus TCP, TLS Inspect, and the HTTP and SNMP trap listeners.
//
// Principle 2: primitives are honest. A TCP node opens a TCP socket. It does not helpfully trim whitespace, assume UTF-8 or normalise line endings unless told to.
//
// Every transport needs an explicit abort adapter that destroys the socket or closes the client when the run deadline fires — not every I/O API accepts a cancellation signal, and a deadline that stops at the API boundary does not satisfy I2.
//
// Deliberate omission (D8): there is no shell/exec node. It turns a monitoring server into a remote code execution surface for anyone with flow-author rights.
package transport
