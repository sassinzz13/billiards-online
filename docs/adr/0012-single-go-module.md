# 0012 — Single Go module, layered feature dependencies

**Status:** Accepted · 2026-08-14

## Context

The repository holds a Go server, an Angular application, database migrations, infrastructure
configuration, and a self-contained billiards engine. The Go side alone spans ten product features,
five game-engine packages, and six platform packages.

Two questions needed answering: how many Go modules, and how are architectural boundaries actually
enforced rather than merely documented?

The second question matters more. ADR 0001 establishes a layer table, but a layer table that nothing
checks is a comment. Import discipline decays under deadline pressure, and by the time the decay is
visible, untangling it is a rewrite.

## Decision

**One Go module at the repository root**, `github.com/sassinzz13/billiards-online`, with the Angular
workspace as a sibling directory under `apps/web`.

```
billiard/
├── go.mod                      ← the only one
├── apps/server/cmd/server/     composition root
├── apps/web/                   Angular workspace (own package.json)
├── internal/                   product features
├── game/                       billiards engine
├── platform/                   technical capability
└── tests/arch/                 boundary enforcement
```

**Package placement is deliberate:**

- `internal/` uses Go's `internal` mechanism, so features are unimportable from outside the module.
- `game/` sits **outside** `internal/`, signalling that it is a self-contained library with no
  knowledge of the platform — and making it trivially extractable later.
- `platform/` also sits outside `internal/`, for the same reason: it is infrastructure, not product.

**Boundaries are enforced by a test.** `tests/arch/boundaries_test.go` reads `go list -json ./...`
and asserts:

1. The ADR 0001 layer table — feature imports point downward only.
2. `game/**` imports the standard library and nothing else.
3. `platform/**` never imports `internal/**` or `game/**`.
4. Nothing imports `apps/server/**`.
5. No package named `shared`, `common`, or `utils` exists.

Roughly 120 lines, zero dependencies, and it fails the build on violation.

## Alternatives considered

**Multiple modules** — one for the server, one for the game engine, perhaps one per feature. Rejected.
Cross-cutting refactors would need `replace` directives during development and version bumps
afterward, turning a single-commit rename into a multi-step release dance. `go test ./...` would no
longer cover everything. It would buy independent versioning for packages that have no independent
consumers. Worth revisiting only if `game/` is genuinely published for outside use.

**A Go workspace (`go.work`) with multiple modules.** Better ergonomics than plain multi-module, but
it still fragments dependency management and adds a file that must stay correct, to solve a problem
that does not exist yet.

**Everything under `internal/`, including the engine.** Marginally stricter. Rejected because the
placement of `game/` is a design signal: it says the engine knows nothing about this application and
could be lifted out whole. That signal is worth more than the additional restriction, particularly
since the arch test enforces the engine's isolation far more strictly than `internal` ever could.

**`go-arch-lint` or a similar architecture linter.** Configured in YAML, more featureful, and a
reasonable choice in general. Rejected under §9: it is a dependency, a config format, and a CI
installation step, to do something that ~120 lines of Go reading our own import graph does natively.
The custom test also lives with the code it constrains and can encode project-specific rules — like
"no package may be named `utils`" — that a general linter would not express as directly.

**Documented conventions with code review as the enforcement.** Rejected on experience. Reviewers
miss imports, especially in large diffs, and a single accepted violation legitimises the next one.
The layer table only holds if something mechanical checks it.

**A monorepo tool** (Nx, Bazel, Turborepo). Rejected as disproportionate. Two applications with
independent, well-understood build systems do not need a build orchestrator. A `Makefile` covers it.

## Consequences

**Good.** One `go.mod`, one dependency set, one version to reason about. `go test ./...` covers the
entire backend. Refactors across features are single atomic commits with no version coordination.
Architectural violations fail the build with a message naming the offending import, rather than
surviving to become load-bearing. The rules live in code next to what they constrain, so they are
discoverable and modifiable by anyone touching the project.

**Costs.** All Go dependencies are shared, so a library needed by one feature is in every binary's
module graph — negligible with a single binary. The arch test must be maintained: adding a genuinely
new dependency direction means editing the layer table deliberately, which is the intended friction.
`apps/web` having its own `package.json` means two dependency ecosystems, unavoidable in any
full-stack repository.

**The friction is the feature.** When a change requires editing `tests/arch/boundaries_test.go`, that
is the signal to stop and ask whether the dependency is right — not to edit the test and move on. If
the layer table genuinely needs to change, update ADR 0001 with the reason.

**Extraction stays open.** If measurement ever justifies splitting out the realtime simulation, the
work is bounded: `game/` already imports nothing, feature dependencies are already one-directional,
and the arch test already proves both. Converting a package into a module is mechanical when its
dependencies are known. That is the whole point of enforcing this now rather than assuming it later.
