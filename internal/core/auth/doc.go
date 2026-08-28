// Package auth is users, sessions, API tokens, roles and the audit log.
//
// Spec §16 roles. Viewer sees dashboards and history but not run captures, which can contain payload data. Operator adds acknowledgement, bounded silences, manual runs and device creation. Author adds flow authoring, capture access and Pack installation, and is explicitly privileged: an author can make agents emit arbitrary traffic within their egress policies. Admin adds credentials, egress policy, users, agent enrolment and revocation, write-capable nodes, sandbox limits and retention.
package auth
