# Project Plan — Deployment Watchdog

Full specification. Read the relevant section before implementing a new component.
Update this file at the end of each phase.

---

## MVP scope

**In:**
- HTTP / TCP health checks with body assertions
- State machine (UNKNOWN / UP / DOWN / DEGRADED / FLAPPING)
- Retry with exponential backoff + jitter
- Confirmation thresholds (N fails → DOWN, M passes → UP)
- Concurrent fan-out with bounded parallelism and timeout
- Check result storage with retention policy

**Out — do not implement, do not suggest:**
- Playwright browser journeys (Phase 6, optional)
- Cost tracking (Phase 4, verify Cost Explorer first)
- Frontend dashboard (Phase 5)
- RDS migration (later, when VPC is being learned)
- Docker (parked)

---

## Check levels

| Level | What | MVP? |
|---|---|---|
| L1 | TCP connect / DNS resolves | 🟢 |
| L2 | HTTP status in expected range | 🟢 |
| L3 | Latency under threshold | 🟢 |
| L4 | Body assertion — contains string, JSON path equals value | 🟢 |
| L5 | Dependency check — app's `/health` reports DB reachable | 🟡 |
| L6 | Real user journey via Playwright | 🔵 |

---

## State machine (🟢 implement this yourself)

```
                  ┌─────────┐
      ┌──────────▶│ UNKNOWN │
      │           └────┬────┘
      │                │ first result
      │      ┌─────────┴─────────┐
      │      ▼                   ▼
   ┌──────────┐             ┌──────────┐
   │    UP    │◀───────────▶│   DOWN   │
   └────┬─────┘  M passes   └─────▲────┘
        │        N fails          │
        │                         │
        │    ┌──────────┐         │
        └───▶│ DEGRADED │─────────┘
             └──────────┘
        (up, latency > warn threshold)


FLAPPING: >N transitions in rolling window → alert once → suppress until stable
```

**Confirmation thresholds:** N=3 consecutive failures → DOWN. M=2 consecutive successes → UP. Asymmetric by design — slow to declare outage, quick to trust recovery.

---

## Database schema

```sql
users
  id, email, created_at

monitors
  id, user_id, name, kind ('http'|'tcp'|'health'),
  target_url, method, expected_status_range,
  body_assertions_json,
  latency_warn_ms, latency_fail_ms,
  interval_seconds, timeout_ms,
  fail_threshold, recover_threshold,
  enabled, created_at

monitor_state
  monitor_id PRIMARY KEY,
  state ('UP'|'DOWN'|'DEGRADED'|'FLAPPING'|'UNKNOWN'),
  consecutive_fails, consecutive_passes,
  last_transition_at, last_checked_at,
  flap_count_window

check_results
  id, monitor_id, checked_at,
  ok, status_code, latency_ms,
  error_class ('dns'|'conn_refused'|'tls'|'timeout'|'status'|'assertion'),
  error_detail, attempt_count
  INDEX (monitor_id, checked_at DESC)
  -- Retention: raw 30 days, then hourly aggregates

incidents
  id, monitor_id, started_at, resolved_at,
  cause_error_class, check_count, notified_at

cost_snapshots
  id, user_id, date, service, amount_cents, currency
  UNIQUE (user_id, date, service)
```

---

## System design

```
        EventBridge (cron: rate(1 minute))
                    │
                    ▼
         ┌─────────────────────┐
         │  Prober Lambda (Go) │
         │  load due monitors  │
         │  fan out w/ errgrp  │
         │  retry w/ backoff   │
         │  write results      │
         │  run state machine  │
         └──────────┬──────────┘
                    │
        ┌───────────┼───────────┐
        ▼           ▼           ▼
     Neon PG    CloudWatch     SNS
                                │
                                ▼
                              n8n
                    ┌───────────┼───────────┐
                    ▼           ▼           ▼
                  Email      Discord      Slack

        Cost Lambda (daily EventBridge)  🔵
                    │
                    ▼
          AWS Cost Explorer API
                    ▼
              cost_snapshots

        Next.js dashboard → API Lambda (Go + chi) → Neon
```

**Design decisions:**
- One prober invocation per minute, handles all due monitors — not one Lambda per monitor
- State transitions happen in the prober, same transaction as result write
- Alerts fire on transition, never on state
- Idempotent by `(monitor_id, checked_at)` — retried invocation doesn't double-write
- Cost Explorer: daily snapshot only, serve from own table, never per request

---

## Phases

### Phase 1 — Local Go binary (1 week) 🟢 MVP
`time.Ticker`, read monitors from Neon, fan-out with `errgroup`, state machine, retry, write results. Runs on your laptop. No AWS.

🎯 Go learning: interfaces, errgroup, context, error handling
🟢 `Checker` interface, state machine, retry, schema, retention

**Milestone: detects one of your real projects going down, on your machine.**

---

### Phase 2 — Lambda + EventBridge (4–5 days) 🟡
Port loop body to Lambda handler. Build for `provided.al2023`. IAM execution role. EventBridge cron rule.

🎯 AWS learning lands here. Budget a frustrating day for IAM — everyone gets one.

---

### Phase 3 — Alerting (3–4 days) 🟡
Flap suppression, incident open/close, SNS on transition, n8n for email/Discord routing.

🟢 flap logic, incident lifecycle

---

### Phase 4 — Cost tracking (3–4 days) 🟡 🔵
**Run DECISIONS.md D9 verification checklist first.** Daily Lambda, Cost Explorer, snapshots, projected month-end, threshold alerts.

---

### Phase 5 — Dashboard (1 week) 🟡 🔵
Status grid, per-monitor history, incidents, cost view, monitor CRUD. Go API with `chi`.
🟡 agent-assisted

---

### Phase 6 — Playwright (optional) 🔵
Hosted browser (Browserless / Browserbase) over websocket. Scripted user journeys.
Docker-based Lambda is the clean solution — revisit when Docker returns.

---

### Phase 7 — Docker (later)
Container-based Lambda solves Playwright binary problem. Architecture already stateless.

---

## Phase completion log

| Phase | Status | Notes |
|---|---|---|
| 1 | ⬜ not started | |
| 2 | ⬜ | |
| 3 | ⬜ | |
| 4 | ⬜ | |
| 5 | ⬜ | |
| 6 | ⬜ | |

---

## Cost Explorer verification checklist (Phase 4)

Before writing any cost-tracking code:
- [ ] Cost Explorer enabled in your AWS account?
- [ ] What does the API cost per request?
- [ ] Data lag — how fresh is the data?
- [ ] What does your account tier expose vs. paid tiers?
- [ ] Is AWS Budgets a sufficient free alternative?

If Cost Explorer is a poor fit: use AWS Budgets alerts + manual entry in `cost_snapshots`. The feature is "know what things cost," not "integrate Cost Explorer."

---

## Risks

| Risk | Mitigation |
|---|---|
| Scope inflation — "small" becomes months | Playwright, RDS, cost tracking all 🔵. Resist adding check types. |
| Learning Go + AWS simultaneously stalls | Phase 1 is local only |
| IAM rabbit hole | Budget a day. Start broad, tighten later. |
| Alert fatigue makes it useless | Flap suppression is Phase 3, not "later" |
| Cost Explorer surprises | Verification checklist before any code |
| `check_results` explodes | Retention policy from the first migration |
