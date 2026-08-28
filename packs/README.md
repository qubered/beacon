# Packs

A Pack is how vendor support exists without vendor code. This is the one place in
the tree where vendor names belong.

Each seed Pack is chosen to prove a part of the primitive set rather than to be a
vendor list, and each is simultaneously a documentation page and a CI test.
**If all seven can be authored entirely through the UI, the primitive set is
right.** They land in M10 — see [the roadmap](../docs/ROADMAP.md).

| Pack | What it proves |
|---|---|
| `network-basics/` | ICMP, TCP connect, HTTP, DNS, certificate expiry — the tier-1 experience |
| `snmp-switch/` | Walk and table, interface counters, PoE, topology feeding the dependency graph |
| `ascii-over-tcp/` | Connection Scope, delimiter framing, IAC stripping, regex extract |
| `projector-control/` | Challenge-response authentication, hashing, multi-turn conversation |
| `rest-graphql-api/` | HTTP auth variants, JSON traversal, the 200-with-errors gotcha |
| `network-discovery/` | ARP/bridge/topology walk, bounded sweep, reverse DNS |
| `binary-over-tcp/` | Build Bytes, length-prefix framing, CRC, bit fields |

## Fixtures are tests

Every Pack ships recorded device responses, and they serve three purposes: they
let someone author a Pack at a desk with the gear locked in a venue; they are the
Pack's regression tests in CI; and when a firmware update changes a response
format, the fixture is what tells you which of the twelve flows broke (D27).

That last point is why fixtures are not optional content. A Pack without them
cannot be maintained by anyone who does not own the gear.

## Licensing

Packs are content, not derivative works of the platform. Pack authors choose
their own licence — the platform's AGPL does not reach them. See
[D31](../docs/decisions/0003-licence.md).
