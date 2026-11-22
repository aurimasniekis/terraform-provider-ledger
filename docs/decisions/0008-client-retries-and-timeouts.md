# ADR-0008: Client retries and timeouts

Status: Accepted (2026-11-22). It replaces the placeholder timeout noted in the client (ADR-0001 left timeout and retry limits open).

## Context

Ledger's operations decision D17 sets the outbound limits: 10 second connect, 30 second total, retries only for idempotent
reads, at most three, exponential backoff with jitter, honouring Retry-After. The shared client had a single 30 second
timeout and no retries.

## Decision

1. **Timeouts.** Connecting is bounded at 10 seconds (`DefaultConnectTimeout`, set on the transport dialer). Each attempt is
   bounded at 30 seconds (`DefaultTimeout`, `http.Client.Timeout`; `Config.Timeout` still overrides it). The Terraform
   operation's own context bounds the whole call, including the waits between attempts.
2. **What is retried.** Only `GET` and `HEAD`. `POST`, `PUT`, `PATCH` and `DELETE` are sent once, always, because repeating
   them could apply a change twice (a lost answer to a create does not mean it failed).
3. **When.** On HTTP 429, 502, 503 and 504, and on transport errors (connection refused or reset, per-attempt timeout) unless the
   caller's context was cancelled. 500 is not retried: it signals a server fault that repeating will not cure. Other 4xx are
   answers, not outages.
4. **How many.** At most three retries (`MaxRetries`), so at most four attempts. D17 says "max 3"; this reads it as three retries.
5. **Backoff.** 250 ms doubling each retry, capped at 10 s, with jitter in the upper half of each step. A valid Retry-After
   (seconds or an HTTP date; non-positive, past and malformed values are ignored) replaces the backoff. A Retry-After above 10 s
   is not waited for: the 429 or 503 answer is returned at once so a Terraform run is not stalled silently.
6. **Verification.** Unit tests with `httptest` cover success after retries, exhaustion, Retry-After parsing, no retry for writes
   and non-transient statuses, connection errors and cancellation. Waits are injected, so tests do not sleep.

## Consequences

Reads survive a brief restart or rate limit. A write that fails transiently is reported, not repeated; Terraform's next plan
or apply reconciles from state. The 10 s ceiling on a single wait is a client choice, not part of D17.
