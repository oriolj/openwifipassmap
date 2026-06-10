# TODO

Loose backlog of things worth doing. Not prioritized, except the last step.

## Before launch

- [ ] **Add Plausible analytics tracking before launching.** Wire up
  privacy-friendly analytics (Plausible) on the public web (landing + share
  pages) so we have visibility into traffic from day one. Self-host as its own
  Coolify resource (not co-deployed with the app). Add the script to the
  server-rendered templates; respect Do-Not-Track.

## Mobile

- [ ] **Bring reviews to the mobile app.** The web now has rate/speed-test
  (POST `/api/spots/{id}/review`) and owner edit; the Capacitor app still only
  creates spots with an initial rating. Add a rate/review action on spot cards
  and an edit screen for own spots.

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
- [x] **Email verification on signup.** Verification link at register (48 h
  TTL, `/verify` page); accounts work immediately but forgot-password only
  emails verified addresses; backfilled accounts marked verified.
- [ ] Resend-verification action for users who lost the signup email.
- [ ] Catch mail locally with **Mailpit** during dev (currently the dev fallback
  just logs the message; wiring SMTP→Mailpit would let us see rendered mail).
- [x] Rate-limit register / login / forgot-password / reset (per-IP token
  buckets; forgot-password is the tight one since each request can send mail).
- [x] `/api/me` exists (username + contribution count).

## Last step (after everything above)

- [ ] **Built-in speed test.** Instead of users typing Mbps numbers by hand,
  measure it in the client: download (and ideally upload) a test payload and
  prefill the review's speed fields with the result. Needs a test endpoint or
  third-party target (self-hosted is privacy-friendlier — e.g. a `/speedtest`
  payload route on the backend with rate limiting), a measuring widget in the
  web review modal + mobile RateForm, and clear UX that it consumes data on
  mobile connections. Ship last: it builds on the whole review pipeline.
