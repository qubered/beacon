// Package link is the agent side of the Core link. The agent dials out; it never listens.
//
// Spec §7.2 and decision D11. One firewall rule per agent VLAN, zero listening ports, works behind NAT and DHCP. Reconnection uses exponential backoff with jitter and the agent does not stop monitoring while disconnected.
//
// Upgrades are agent-initiated (spec §7.5): Core advertises a build, the agent fetches, verifies a signature, drains and restarts within a window, keeps the previous build, and rolls back automatically if the new one fails to reconnect. An agent that upgrades itself into a brick, on a VLAN, in a venue, is a drive across town.
package link
