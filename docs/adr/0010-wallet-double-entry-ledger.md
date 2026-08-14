# 0010 — Double-entry immutable wallet ledger

**Status:** Accepted · 2026-08-14

## Context

Players wager virtual currency on matches. The currency is virtual and there is no real-money
processing (§2), but the accounting requirements are the same as real money: balances must be
correct, every change must be explicable, and a retry must never pay twice.

The constitution is explicit that the wallet must not be a mutable `users.balance` column (§31),
should use double-entry concepts (§32), and that settlement must be idempotent (§33, §58).

There is also a forward-looking constraint: real payment providers may be integrated later, and that
must not require rewriting wallet accounting, wagering, settlement, match logic, or leaderboards
(§2, §34).

## Decision

**An immutable double-entry ledger.** Balances are derived from entries, never assigned.

```
wallets               one per user, holds a materialised balance for fast reads
ledger_accounts       user wallets, wager escrow accounts, system accounts
ledger_transactions   one per business event, carries the reason and idempotency key
ledger_entries        the actual movements — signed amounts against accounts
```

Rules that hold without exception:

- **Every transaction's entries sum to exactly zero**, enforced by a database constraint (deferrable,
  checked at commit) plus a trigger — not by application code alone.
- **History is append-only.** No `UPDATE`, no `DELETE` on `ledger_transactions` or `ledger_entries`,
  ever. A correction is a new compensating transaction.
- **Amounts are `BIGINT` minor units.** No floating point touches money anywhere in this system.
- The materialised balance on `wallets` is an optimization, reconciled against `SUM(amount)` over
  entries by an invariant test.

Money movement:

```
Reserve:  player wallet −100,  wager escrow +100
Settle:   wager escrow  −200,  winner wallet +200
Refund:   wager escrow  −100,  player wallet +100
```

**Idempotency** is a database constraint: `UNIQUE (wager_id, idempotency_key)` on
`wager_settlements`. A retry hits the constraint and returns the *original* result. It cannot pay
twice — not because the code is careful, but because the schema forbids it. Concurrency uses
`SELECT … FOR UPDATE` on the wager row inside the settlement transaction.

**Provider independence:** `internal/wallet` exposes `Credit` and `Debit` and knows nothing about
where money came from. That is the entire seam for future payment providers. The game never learns a
provider exists.

## Alternatives considered

**A mutable `users.balance` column with `UPDATE … SET balance = balance ± n`.** Prohibited by §31,
and rightly. It answers "what is the balance" and nothing else. When a balance is wrong — and it will
be — there is no way to find out why. There is no audit trail, no way to reconcile, and no way to
detect that a bug has been silently corrupting balances for weeks.

**Single-entry ledger** — an append-only log of `(wallet, delta, reason)`. A real improvement over a
mutable column: it gives history and auditability. Rejected because nothing enforces that money is
conserved. A bug that credits without a matching debit creates currency from nothing, and no
constraint notices. Double-entry makes conservation a checkable invariant: if the sum is not zero,
the transaction does not commit.

**Event sourcing with a projection.** Rejected as heavier than needed. A double-entry ledger already
is an append-only event log, with the significant advantage that the "events" are a well-understood
accounting model rather than a bespoke one. Full event sourcing adds projection rebuilds and
eventual-consistency questions to solve a problem already solved.

**Application-level idempotency** — check whether a settlement exists, then insert. Rejected as a
textbook race: two concurrent settlements both check, both find nothing, both insert. The unique
constraint is the only reliable mechanism, because it is enforced at the point of commit.

**Storing money as `NUMERIC`.** Defensible, and correct for arbitrary precision. Rejected in favour of
`BIGINT` minor units: integer arithmetic is exact by construction, faster, and removes any question
about rounding behaviour. Virtual currency has no sub-unit requirement.

**Floating point for amounts.** Rejected absolutely. `0.1 + 0.2 != 0.3`, and money bugs of this kind
are silent and cumulative.

## Consequences

**Good.** Every balance change is explicable: the ledger says what moved, when, why, and under which
transaction. Money cannot be created or destroyed by a bug, because the sum-to-zero constraint
refuses the transaction. Settlement is idempotent by schema, so retries — from a network blip, a
crash, or a redelivery — are safe. Reconciliation is a query, and it runs as a test. Adding a real
payment provider later means adding an adapter that calls `Credit`/`Debit`; nothing in wagering,
matches, or leaderboards changes.

**Costs.** More tables and more writes than a balance column — a settlement is a transaction plus
two entries rather than two `UPDATE`s. This is the correct trade. Reading a balance from entries is a
`SUM`, which is why the materialised balance on `wallets` exists, and that in turn is a denormalization
that must be kept honest by the reconciliation test. Every money operation must be reasoned about in
terms of which account is debited and which is credited, which is unfamiliar at first and correct
forever after.

**Testing is not optional here.** The invariants — sum-to-zero, balance reconciliation, duplicate
settlement paying once, and N concurrent settlements paying once — must be tested before wagering
ships (risk R6). Wallet bugs are silent and permanent: nobody reports having received money they
should not have.

**Never.** No `UPDATE` or `DELETE` on ledger rows, under any circumstance, including "fixing" test
data in a shared environment. Corrections are compensating transactions.
