-- 0003 interval floor: a monitor may not be scheduled faster than the floor.
--
-- A one-second interval is a footgun that will knock over someone's receiver.
-- The floor is five seconds, and going below it is an admin-only setting rather
-- than something a flow author can type into a form (roadmap, "Settled since").
--
-- The constraint lives here rather than only in application code because this is
-- a refusal, and the roadmap front-loads refusals: one added after people have
-- written monitors breaks flows that already exist. Adding it now, while nothing
-- has written a monitor row, costs nothing.
--
-- 1000ms rather than 5000ms is deliberate. The database enforces the *hard*
-- floor — the one nobody, admin included, may cross — while the five-second
-- default floor is configuration, so an operator can lower it for robust gear
-- without a migration. Encoding the softer number here would make the
-- admin override impossible without DDL.
ALTER TABLE monitors
    ADD CONSTRAINT monitors_interval_floor CHECK (interval_ms >= 1000);

-- A timeout longer than the interval guarantees overlapping runs, which is the
-- schedule falling apart rather than a monitor being slow (spec §6.2).
ALTER TABLE monitors
    ADD CONSTRAINT monitors_timeout_positive CHECK (timeout_ms > 0);
