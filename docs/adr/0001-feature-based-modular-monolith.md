# 0001 — Feature-based modular monolith, strictly layered

**Status:** Accepted · 2026-08-14

## Context

The platform spans authentication, users, rooms, matchmaking, matches, leaderboards, a wallet,
wagering, and a realtime game server. That is enough surface area that organization stops being a
matter of taste and starts determining whether the codebase remains workable.

Two failure modes are worth naming. The first is horizontal layering — `controllers/`, `services/`,
`repositories/`, `models/` — where adding one feature means touching four directories and no
directory tells you what the product does. The second is premature microservices, where the same
features become network calls, and a room join that should be one transaction becomes a distributed
consistency problem.

The engineering constitution mandates feature-based architecture (§4) and warns against premature
microservices (§8).

## Decision

A **feature-based modular monolith**, organized by product capability, with features arranged in
strict layers.

Backend features live in `internal/`: `auth`, `users`, `lobby`, `rooms`, `matchmaking`, `matches`,
`leaderboards`, `wallet`, `wagering`, `realtime`. Each owns its handlers, service logic,
persistence, SQL, validation, types, and tests.

Features are assigned to layers, and **imports may only point downward**:

| Layer | Features |
|---|---|
| L6 | `realtime` |
| L5 | `lobby`, `matchmaking` |
| L4 | `rooms` |
| L3 | `matches` |
| L2 | `wagering` |
| L1 | `auth`, `leaderboards` |
| L0 | `users`, `wallet` |

**Same-layer imports are forbidden as well as upward ones.** Two features sharing a layer are
declared independent, so an import between them is either a mistake or evidence that one belongs
higher. `lobby` moved from L4 to L5 during Phase 1 for precisely this reason: it lists rooms, so it
sits above `rooms` rather than beside it.

Cross-feature communication uses **consumer-side interfaces**: the consumer declares the narrow
interface it needs, the provider is a plain struct that satisfies it, and `apps/server/cmd/server/main.go`
wires them together.

```go
// internal/matches/service.go — matches declares what IT needs
type WagerSettler interface {
    Settle(ctx context.Context, matchID uuid.UUID, winner SideID, idempotencyKey string) error
}
```

`internal/wagering` never imports `internal/matches`.

Enforced by `tests/arch/boundaries_test.go`, which reads `go list -json ./...` and fails the build on
violation. There is no `shared/`, `common/`, or `utils/` package.

## Alternatives considered

**Horizontal technical layers.** Familiar from many tutorials and frameworks. Rejected because it
scatters every feature across four directories, makes ownership invisible, and turns every
"where does this go?" question into a guess. Explicitly prohibited by §4.

**Microservices from the start.** Would give hard boundaries and independent deployment. Rejected
because it converts in-process function calls into network calls with no measured need, makes
transactional operations like a room join genuinely hard, and multiplies operational burden for a
project with no users yet. The constitution's §8 position — clean seams now, extract only if
measurements justify it — is the right one.

**Feature packages without enforced layering.** Simpler to set up. Rejected because "don't create
cycles" as a convention decays. Go will reject an actual import cycle, but the graph degrades into
mutual dependency long before that, and by then untangling it is a rewrite. The layer table makes the
constraint checkable.

**A shared `contracts/` package holding cross-feature interfaces.** A common approach. Rejected
because it becomes the dumping ground §6 warns about, and it inverts the Go idiom — interfaces belong
with the consumer, not in a central registry.

**An event bus for decoupling.** Rejected by §5 and on its merits: it converts compile-time errors
into runtime ones, makes control flow invisible in a stack trace, and adds indirection for a problem
plain function calls already solve.

## Consequences

**Good.** Feature ownership is obvious from the directory tree. Adding a feature means adding a
directory, not editing four. Cycles are structurally impossible, not merely discouraged. Cross-feature
calls are ordinary Go function calls — no serialization, no network, no reflection, and they show up
in stack traces and profiles. Any feature could later be extracted, because its dependencies are
already explicit and one-directional.

**Costs.** The layer table must be maintained; when a genuinely new dependency appears, the table
needs a deliberate update rather than a quiet import. Consumer-side interfaces mean the same concept
may be described slightly differently by two consumers — acceptable, and often clearer than a shared
type that serves neither well. Some duplication will occur, and §6 says to prefer it over the wrong
abstraction.

**Watch for.** The layering assumes `matches` sits above `wagering` and `leaderboards`. If a
requirement appears that needs the reverse, do not add an event bus to dodge it — reconsider the
layer assignment and record why in this ADR.
