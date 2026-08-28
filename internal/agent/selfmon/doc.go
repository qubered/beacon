// Package selfmon exports agent-local health: execution counts, spool depth, link state, scheduler lag, clock skew.
//
// Tier 1 of spec §12. Scraped directly where the network allows; otherwise it reaches Core in heartbeats and Core re-exports it labelled by agent.
package selfmon
