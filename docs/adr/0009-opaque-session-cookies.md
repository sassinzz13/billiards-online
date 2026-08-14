# 0009 — Opaque session cookies, not JWT

**Status:** Accepted · 2026-08-14

## Context

Users authenticate over HTTP and then open a WebSocket that must carry the same identity. Sessions
gate access to matches, wallets, and wagers, so revocation matters: a compromised session, a banned
account, or a logout must stop working immediately, not at token expiry.

There is a WebSocket-specific constraint that shapes this decision. **Browsers cannot set custom
headers on a WebSocket handshake.** The `WebSocket` constructor takes a URL and an optional
subprotocol — there is no way to send `Authorization: Bearer …`. Any bearer-token scheme must
therefore smuggle the token through a query parameter, a subprotocol field, or a first message, each
with its own problems.

## Decision

**Argon2id password hashing, and opaque random session tokens delivered as cookies.**

Passwords: Argon2id, `t=3, m=64 MiB, p=4`, 16-byte random salt, per-hash parameters stored with the
hash so they can be raised later without invalidating existing passwords.

Sessions: 32 bytes from `crypto/rand`, stored **SHA-256 hashed** in `auth.sessions` — a database read
never yields a usable token. Delivered as:

```
Set-Cookie: sid=<base64url>; HttpOnly; Secure; SameSite=Lax; Path=/; Max-Age=...
```

HTTP requests: middleware reads the cookie, hashes it, looks up the session, checks expiry and
revocation, binds `userID` to the request `context.Context`.

WebSocket upgrade: **the cookie is sent automatically**, because Traefik puts the web app and the API
on the same origin. The upgrade additionally enforces a **strict Origin allowlist** — this is what
actually prevents cross-site WebSocket hijacking, since `SameSite` does not reliably protect
WebSocket handshakes.

**Phase 1 note — the same-origin claim is now literally true.** The original plan put the API on
`api.billiards.localhost`, which is a *different origin* from `billiards.localhost`. That would have
forced a `Domain=.billiards…` cookie (widening its scope to every subdomain), weakened `SameSite=Lax`,
and required CORS. Phase 1 therefore routes **one host by path** — `/` to the web container,
`/api` and `/ws` to the server — which makes this decision work as written rather than approximately.
See MEMORY.md §10a.

The allowlist is implemented as exact string equality in `config.AllowsOrigin`, with a test asserting
that `http://billiards.localhost.evil.com` and `http://evil.com/http://billiards.localhost` are both
rejected. A prefix or suffix comparison is the classic way this check gets bypassed.

Authentication and authorization stay separate (§36). A valid session proves identity and nothing
else. Every action re-checks authorization in context: is this user in this match, on this side, is
it their turn, is this their wallet.

## Alternatives considered

**JWT access token plus refresh token.** The reflexive modern choice. Rejected on several counts.

Revocation is the core problem: a stateless token is valid until it expires, so immediate revocation
requires a denylist checked on every request — which is a database lookup, which is the thing JWTs
were supposed to avoid. Having paid that cost, the statelessness benefit is gone.

The claimed advantage is avoiding a database read per request. But every authenticated request in
this system already touches the database, so one indexed lookup on a hashed token adds no meaningful
latency.

JWT also brings signing key management and rotation, and a class of implementation vulnerabilities
(`alg: none`, confusion between HMAC and RSA verification) that opaque tokens simply do not have.

And it does not solve the WebSocket problem: the browser still cannot send an `Authorization` header
on a handshake, so a ticket or query parameter is needed anyway.

**Session cookie for HTTP, plus a short-lived single-use WebSocket ticket.** A `POST
/api/v1/realtime/ticket` returning a 30-second single-use token passed as a query parameter. Genuinely
robust — immune to cross-site WebSocket hijacking by construction, and works cross-origin. Rejected
for now as unnecessary machinery: an extra endpoint, an extra table, and an extra round trip before
every connect, to solve a problem that a strict Origin allowlist already solves in a same-origin
deployment. **Recorded as the explicit escape hatch** if a cross-origin deployment ever becomes
necessary; the design does not preclude it.

**Token in `localStorage`, sent as a query parameter.** Rejected. `localStorage` is readable by any
XSS, whereas `HttpOnly` cookies are not. Query parameters land in access logs and proxy logs.

**bcrypt for passwords.** Adequate, but rejected in favour of Argon2id — the current recommendation,
memory-hard, and specifically resistant to GPU and ASIC attacks. bcrypt also silently truncates input
beyond 72 bytes, which is a sharp edge worth avoiding. scrypt was considered and is fine; Argon2id is
the more current choice with better-understood parameters.

## Consequences

**Good.** Revocation is immediate and unconditional — logout, ban, or compromise takes effect on the
next request. `HttpOnly` means XSS cannot read the session token. No signing keys to manage or
rotate. The WebSocket upgrade needs no special handling: the cookie is simply sent. Session listing
and "log out everywhere" are trivial to add, because sessions are rows. A database dump does not
yield usable tokens.

**Costs.** One indexed database lookup per authenticated request — measured and accepted as
negligible against the queries each request already makes. Sessions are server state, so horizontal
scaling requires a shared database, which is already the case. Cookies mean CSRF must be considered
for state-changing HTTP endpoints; `SameSite=Lax` covers the common cases and explicit protection
covers the rest.

**The Origin allowlist is load-bearing.** It is the control that prevents cross-site WebSocket
hijacking. It must be validated as strict-equality against a configured list — never a substring or
suffix match, which is the classic way this check gets bypassed. This deserves a dedicated test.

**Revisit if.** A cross-origin deployment becomes necessary, or a third-party client needs access. In
either case the ticket approach above is the intended path, and it composes with what is already
built rather than replacing it.
