# Architecture Decision Records

Each ADR records one decision: the **context** that forced it, the **decision** itself, the
**alternatives considered**, and the **consequences** — including the bad ones.

An ADR exists so a future contributor can tell the difference between "this was chosen deliberately"
and "this is how it happened to get written." If you find yourself re-litigating something, read the
ADR first; if the ADR's context no longer holds, write a new ADR that supersedes it rather than
editing history.

All twelve below were accepted in Phase 0 on 2026-08-14.

| # | Decision |
|---|---|
| [0001](0001-feature-based-modular-monolith.md) | Feature-based modular monolith, strictly layered |
| [0002](0002-pgx-without-orm-or-sqlc.md) | pgx/v5 with explicit SQL — no ORM, and no sqlc |
| [0003](0003-server-authoritative-gameplay.md) | Server-authoritative gameplay |
| [0004](0004-custom-billiards-physics.md) | Custom billiards physics at fixed timestep |
| [0005](0005-precomputed-shot-trajectory.md) | Precomputed shot trajectory transport |
| [0006](0006-websocket-protocol-envelope.md) | Versioned WebSocket protocol envelope |
| [0007](0007-match-actor-ownership.md) | One goroutine owns one match |
| [0008](0008-coordinate-system.md) | Y-up right-handed metric coordinates |
| [0009](0009-opaque-session-cookies.md) | Opaque session cookies, not JWT |
| [0010](0010-wallet-double-entry-ledger.md) | Double-entry immutable wallet ledger |
| [0011](0011-uuidv7-identifiers.md) | UUIDv7 identifiers |
| [0012](0012-single-go-module.md) | Single Go module, layered feature dependencies |
