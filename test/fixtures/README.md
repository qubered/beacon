# Fixtures

Recorded device responses, used by the acceptance harness and by `beaconctl flow
run --fixture`.

Vendor names are permitted here — this is a recording of what a real device said,
and pretending otherwise would make the fixture useless as documentation. Anywhere
outside `packs/`, `test/fixtures/` and tests, a vendor name means the abstraction
has failed and CI will say so.

A fixture is bytes plus enough metadata to replay them: the transport, the framing
that produced each read, and the timing where timing matters (a quiet-period read
cannot be replayed without it).
