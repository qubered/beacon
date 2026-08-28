// Package executor binds a monitor to the engine: builds run context, applies retries, and hands results to the spool.
//
// Run context (spec §6.2) is read-only and carries the device, the monitor, run metadata, the previous run's stored values, persisted key/value state and a secret() accessor. Monitor vars override device vars override flow defaults — this is how one flow serves fourteen devices.
//
// Retries are a monitor property, not a node property, and they re-run the whole flow.
//
// Execution and persistence are decoupled (principle 8): results go to the spool and a writer drains it. Monitoring never stops because storage is unhappy.
package executor
