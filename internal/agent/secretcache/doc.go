// Package secretcache holds credentials for this agent's assigned devices, in memory only by default.
//
// Spec §16. An agent receives only the credentials for its assigned devices. The trade-off is real and must be visible in the UI: an agent that reboots while Core is unreachable cannot run credentialed monitors until it reconnects, though uncredentialed ones keep working.
//
// An opt-in encrypted local cache exists for sites that prefer offline-boot resilience. It is clearly flagged as a weaker posture and defaults to off.
package secretcache
