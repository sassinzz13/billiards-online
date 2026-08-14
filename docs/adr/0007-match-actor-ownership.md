# 0007 — One goroutine owns one match

**Status:** Accepted · 2026-08-14

## Context

An in-progress match has mutable state — ball positions, whose turn it is, the shot clock, scores,
rule state — touched concurrently by two to four player connections plus spectators, a shot timer,
and disconnect handling.

The constitution requires deliberate concurrency (§40): every long-lived goroutine needs an owner,
cancellation, shutdown behaviour, and bounded resource usage. It also requires that a single
authoritative simulation instance own a match at any time (§19), and that a slow client never block
the simulation (§23).

## Decision

**One goroutine per active match owns that match's state exclusively.** State is never shared, only
messaged.

```
internal/matches
    registry:  map[uuid.UUID]*actor  behind sync.RWMutex

    actor (one goroutine):
        owns MatchState exclusively — NO mutex on game state
        bounded inbound command chan (32)
        shot timer via time.Timer
        context.Context for cancellation
        broadcasts through each connection's bounded outbound chan (64)
```

The actor loop is a `select` over its command channel, its shot timer, and its context. Commands
arrive from the realtime router (shot requests, disconnects, spectator joins).

Because shots are precomputed rather than ticked (ADR 0005), **the actor is blocked in `select`
almost all of the time**. There is no per-match ticker anywhere in this system. A match costs a few
kilobytes of goroutine stack and channel buffer, not CPU.

Lifecycle is explicit: created when the match starts, cancelled when it completes or is abandoned,
removed from the registry on exit. A test asserts no goroutine survives match completion.

**Backpressure:** if a connection's outbound queue fills, that connection is closed with a policy
code. Not drained, not dropped. Shot results are large and non-idempotent, so losing one desyncs the
client — close-and-resync is the only correct behaviour, and a slow client can never block the actor.

Through Phase 12, **one server process owns all matches.**

## Alternatives considered

**Shared `MatchState` behind a mutex.** The obvious approach. Rejected because a match involves
multi-step operations — validate turn, simulate, apply rules, update state, broadcast — that must be
atomic with respect to each other. Doing that with a mutex means holding it across the whole
operation, which is a single-writer design with extra steps and worse failure modes: forgotten
unlocks, lock ordering against the registry, and deadlock risk when broadcasting under lock. Single
ownership gives the same serialization with none of the hazards.

**A worker pool processing match commands.** Rejected because state would have to move between
workers or live behind a lock, reintroducing the problem. Ownership is the point.

**One goroutine per connection mutating shared state.** Rejected outright: it is the mutex approach
with more writers.

**Actors plus a real-time ticker per match.** This is what a streaming design (see ADR 0005) would
require. Rejected along with streaming: thousands of goroutines waking 120 times per second is real
CPU cost, and it buys nothing when shots are precomputed.

**Dropping messages under backpressure instead of closing.** Rejected. It would be right for
idempotent state snapshots that supersede each other, but `shot.result` is not idempotent — a client
that misses one has no way to recover the motion, and the desync is silent. Closing is loud and
recoverable.

**Distributed match ownership from the start** — Redis or a message broker for cross-instance state.
Rejected by §8 and §39. It solves a scaling problem that has not been measured, at the cost of
substantial complexity in the most correctness-sensitive part of the system.

## Consequences

**Good.** No mutex on game state at all, so a whole class of race conditions cannot occur. Operations
are naturally atomic — the actor processes one command to completion before the next. Every goroutine
has an owner and a cancellation path, satisfying §40 by construction. Bounded queues everywhere mean
no unbounded memory growth. A slow client is isolated: it affects only itself. Because shots are
precomputed, idle matches are nearly free, so thousands of concurrent matches is a memory question
rather than a CPU one.

**Costs.** Every interaction with a match is asynchronous — a caller wanting a reply sends a command
carrying a reply channel. This is more ceremony than a method call. Debugging is message-flow
debugging rather than stack-trace debugging. The registry mutex is a shared point, though contention
is low: it is taken briefly on match start, lookup, and completion, never during simulation.

**Limitation.** Single-instance ownership caps horizontal scaling (risk R2). Extraction is not blocked
— state is already isolated behind a message interface, which is exactly what a distributed version
needs — but it would require sticky routing or a match locator. Deliberately deferred until
measurement justifies it.

**Watch for.** Blocking operations inside the actor loop. A database write in the shot path would
serialize the match behind disk latency. Persistence is asynchronous and off the simulation path for
exactly this reason (§12: never perform blocking database operations inside the simulation loop).
