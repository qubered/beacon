# Security model

Be blunt about what this is: **a system that sends arbitrary bytes to arbitrary
hosts on a schedule, with payloads composed in a browser, from machines
distributed across every VLAN.** That is a genuinely dangerous shape. It is also
the shape that makes the product work. Nothing in spec §16 is optional polish.

This document is the operator-facing companion to that section. It exists to be
read before deployment, and it is written to *under*-claim.

## The stated assumption

**Flow authors are semi-trusted staff, not the public. Agents are inside the trust
boundary. A compromised agent can lie about results.**

Say this out loud, because over-claiming the isolation is how someone ends up
exposing this to the internet.

## What the sandbox does not promise

The tier-2 transform sandbox is **defence in depth against mistakes and
compromised accounts, not a hostile-multi-tenant boundary.** It has a hard memory
cap, an enforced CPU deadline backed by the thread rather than a cooperative
interrupt, and only serialised data crosses its boundary. It is not a barrier you
should place between yourself and an adversary you have invited in.

## What sealed frames do not promise

A secret resolves into a **sealed frame**: non-capturable (masked by value scan,
so a secret composed into a larger payload is masked in the hex dump too),
non-exportable, consumable only by transports, hashing, HMAC and payload
composition, and never readable from an expression or the sandbox.

A flow author can *use* a password and compose it into a digest. They can never
*see* one, and no capture ever contains one.

**The honest limitation:** an author can still HMAC a secret and transmit the
result, which is exfiltration by another name. Sealed frames stop accidental
disclosure, not a determined author. The mitigation is granting credentials to
the devices they belong to rather than making every credential usable from every
flow — and saying this plainly rather than implying more than the mechanism
delivers.

## The master key

Two rules, and both of them matter:

- **Back up the encryption master key separately from the database.** If it lives
  only in an environment variable and is lost, every credential is unrecoverable.
- **Do not back it up alongside the database.** If it lives in the same backup as
  the data it protects, the encryption is decorative.

## Egress control

The single most important control, and the one most similar products skip. A
default-deny allowlist of ranges, ports and protocols; a hard denylist that cannot
be overridden — loopback, link-local, metadata addresses, the platform's own
management addresses and the database, **including their IPv6 equivalents and
IPv4-mapped IPv6 forms**; resolve-then-pin with no re-resolution between check and
connect; and a redirect to a denied host as a hard failure rather than a follow.

**The policy is authoritative on the agent.** Core can propose a change, which the
agent surfaces for operator approval with a diff. Core cannot widen it silently.
This is the control that makes a compromised control plane survivable rather than
building-wide.

Write-capable nodes and multicast are likewise locally enabled per agent, never
remotely enabled.

## The sweep node

A bounded CIDR sweep is a legitimate discovery primitive and also a port scanner.
Concurrency and total addresses are capped, it is rate-limited, every sweep is
logged with its range, and a sweep outside a Pack's declared scope is a
publish-time failure rather than a runtime truncation.

## Roles

| Role | Capabilities |
|---|---|
| **Viewer** | Dashboards, status, history. **No run captures** — they can contain payload data. |
| **Operator** | Viewer, plus acknowledge incidents, create bounded silences, run a monitor manually, add devices. |
| **Author** | Operator, plus create/edit/publish flows, view captures, install Packs. **Explicitly privileged:** an author can make agents emit arbitrary traffic within their egress policies. |
| **Admin** | Author, plus credentials, egress policy, users, agent enrolment and revocation, write-capable nodes, sandbox limits, retention. |

Everything an author or admin does is audit-logged with a before/after diff.
