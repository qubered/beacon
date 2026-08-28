// Package ratelimit enforces outward rate limits against fragile gear.
//
// Principle 9 and spec §8. Rate limiting is enforced by the engine, not left to the flow author's judgement: a per-device maximum concurrent connection count (default 1 for session-capable devices) and a per-device minimum request interval as a token bucket spanning all monitors bound to it.
//
// A monitor blocked on the bucket records a throttled outcome, does not count as a failure, and increments a counter — a non-zero throttle count means someone has over-scheduled a device and needs to know.
package ratelimit
