# Decisions — Deployment Watchdog

Every architectural decision, with the rejected alternative. Settled unless marked `open`.
Append new decisions using the template at the bottom. Never delete — mark superseded.

---

### D1 — Deliberately no AI in the core
**Status:** active

This project exists partly to show that useful infrastructure doesn't need an LLM. The checking engine, state machine, and alerting are all deterministic. Optional much later: plain-English incident summary. That's the ceiling.

**Rejected:** adding an LLM to the checking loop, AI-based anomaly detection in v1.

---

### D2 — Go is the entire backend, not just the prober
**Status:** active

A 300-line prober gives the cost of learning Go without the portfolio benefit. If the motivation is that job postings ask for Go, a token component doesn't answer it. Go is the prober, the API, and the workers.

No framework — stdlib `net/http` + `chi` for routing + `database/sql` with `pgx`. Reaching for Gin or Echo early hides the thing you're trying to learn: interfaces, explicit error handling, `context` propagation, goroutines, the standard library.

**Rejected:** Go only for the prober with another language for the API; using Gin or Echo from the start.

---

### D3 — Phase 1 is local Go with no AWS
**Status:** active

Learning Go and AWS simultaneously is where people stall. Build the state machine and concurrency on your machine where you can debug normally, then port to Lambda. The ticker goes away; EventBridge becomes the clock. Everything else is the same code.

**Rejected:** starting with Lambda on day one.

---

### D4 — One prober invocation handles all due monitors
**Status:** active

Not one Lambda per monitor. In-process fan-out with `errgroup` and a semaphore is cheaper, simpler, and sufficient at this scale. One invocation per minute, all monitors, bounded concurrency, hard timeout on the batch.

**Rejected:** per-monitor Lambda invocations.

---

### D5 — State machine with asymmetric confirmation thresholds
**Status:** active

N consecutive failures before declaring DOWN (default 3), M consecutive successes before declaring recovery (default 2). Asymmetric on purpose: slow to declare an outage, quick to trust recovery. Single checks are noise.

States: UNKNOWN → UP / DOWN / DEGRADED / FLAPPING.

**Rejected:** immediate state change on first result; two-state UP/DOWN only.

---

### D6 — Alerts fire on transition, never on state
**Status:** active

Alerting on "monitor is DOWN" every minute is how you get muted. Alert once when the state transitions to DOWN, once when it recovers. Flap suppression handles the bounce case.

**Alert fatigue is the actual failure mode of this product.** A watchdog that cries wolf gets muted, and a muted watchdog is worth nothing.

**Rejected:** alerting every check cycle while DOWN.

---

### D7 — Flap suppression
**Status:** active

A service bouncing every 3 minutes must not send twenty alerts. Track transitions in a rolling window; above a threshold, mark FLAPPING, alert once, suppress until stable.

**Rejected:** no flap suppression (alert fatigue), marking flapping as DOWN (misleading).

---

### D8 — Neon first, RDS later
**Status:** active

RDS needs a VPC, and Lambda-in-a-VPC is where people lose a weekend to NAT gateways and security groups. Neon is a connection string. Ship the working thing on Neon, migrate to RDS deliberately when VPC networking is actually being learned — not as an accident on the way to something else.

**Rejected:** starting with RDS.

---

### D9 — Cost Explorer verified before designing around it
**Status:** open — verify in Phase 4

Cost Explorer requires explicit activation, charges ~$0.01/request, and data can lag ~24h. Verify against the real account before designing cost tracking.

**Fallback if it's a poor fit:** AWS Budgets alerts + manual entry. The feature is "know what things cost," not "integrate Cost Explorer."

**Design rule regardless:** snapshot once daily, serve from `cost_snapshots` table. Never call Cost Explorer per page load.

---

### D10 — Playwright deferred to Phase 6 / optional
**Status:** active

Playwright needs a browser binary that doesn't fit plain Lambda without a chunky custom layer. The right solution (container-based Lambda) requires Docker, which is parked. Defer to Phase 6 or when Docker returns.

Playwright is the best feature in the product — it's just not worth blocking v1 on.

**Rejected:** shipping Playwright in v1 with a fragile Lambda layer workaround.

---

### D11 — Docker parked, not excluded
**Status:** active

Blocked on local storage. Container-based Lambda is the natural home for Playwright. Architecture stays stateless so containerising is a packaging change, not a redesign.

**Rejected:** designing around Docker's permanent absence.

---

### D12 — Retry with backoff before recording a failure
**Status:** active

One timeout is noise. Three in a row is an outage. Within a single check: retry with exponential backoff and jitter (3 attempts max) before recording a failure. Distinguish error classes — DNS failure, connection refused, TLS error, timeout, bad status are different problems.

**Rejected:** recording every single-attempt timeout as a failure.

---

## Template

```
### D<n> — <one-line decision>
**Status:** active | open | superseded by D<n>

<why, 2–4 sentences>

**Rejected:** <alternative and why not>
```
