# TODO

Loose backlog of things worth doing. Not prioritized.

## Infrastructure

- [ ] **Real migration mechanism.** Schema changes now live in two places:
  `migrations/schema.sql` (idempotent `CREATE … IF NOT EXISTS`, run by `Migrate`)
  and imperative code (`store.EnsureUserEmail` — probe `table_info` → `ALTER` →
  index → backfill). Fine for a one-off column add; it'll get fragile as changes
  accumulate. Next schema change, introduce a lightweight versioned migration
  table (`version INT, applied_at INTEGER`) and time-ordered migration steps.

## Auth / signup

- [x] **Email on accounts + Resend + password reset.** Registration now requires
  a valid email; `internal/email` sends via Resend's HTTP API (logs instead when
  `RESEND_API_KEY` is unset). `POST /api/auth/forgot-password` emails a single-use
  magic link (1 h TTL) that lands on the server-rendered `/reset` page →
  `POST /api/auth/reset-password`. Mail is sent off the hot path (goroutine).
  Pre-email accounts are backfilled to `BACKFILL_EMAIL` on boot.
- [ ] **Email verification on signup** (confirm the address actually belongs to
  the registrant before it's usable for reset). Not done yet — emails are trusted
  as-entered.
- [ ] Catch mail locally with **Mailpit** during dev (currently the dev fallback
  just logs the message; wiring SMTP→Mailpit would let us see rendered mail).
- [ ] Consider rate-limiting register / login / forgot-password (currently
  unthrottled — forgot-password in particular can be used to spam an address).
- [ ] Add a `/me` endpoint or surface the username on the reset page so
  account-recovery ("what's my username") is self-serve.
