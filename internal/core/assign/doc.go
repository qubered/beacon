// Package assign binds monitors to agents and gates assignment on declared capabilities.
//
// Invariant I5: a device is assigned to exactly one agent, enforced by the schema. Monitors inherit their device's agent. Two executors must never hold sockets to the same device.
//
// Decision D16: no automatic failover. Automatic takeover introduces split-brain for a failure mode that is rare and immediately visible; manual reassignment is one click.
//
// Spec §7.5: Core refuses to assign a monitor whose flow needs a capability the agent lacks, and says so in the UI — "3 monitors cannot be assigned to agent lx: it does not implement Checksum".
package assign
