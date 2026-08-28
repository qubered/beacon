// Package fleet is the agent registry: enrolment, approval, certificates, capabilities, revocation and clock skew.
//
// Spec §16 enrolment. A one-time, short-lived, single-use token scoped to an intended agent name and egress policy; the agent generates its keypair locally and the private key never leaves it; Core issues a short-lived client certificate and burns the token. Enrolment waits in a pending approval queue by default, because "a small computer appeared on the network and joined the monitoring system" should be a decision, not an event. Revocation is immediate and instructs the agent to wipe local state.
package fleet
