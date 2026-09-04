# Development Rules — Deployment Watchdog

How code gets written in this project.

---

## Build split

| Mark | Meaning |
|---|---|
| 🟢 | Human writes this. Agent does not implement it. |
| 🟡 | Agent-assisted. Human reviews and must explain every decision. |
| 🔴 | Agent handles it. Boilerplate, CRUD, config, migrations, docs. |

### 🟢 — do not implement these

| Component | Why it's yours |
|---|---|
| State machine + confirmation thresholds | The core product logic |
| Flap suppression | Alert fatigue prevention — the real failure mode |
| Retry / backoff / error classification | Transient vs real failure distinction |
| Concurrency + context handling | Where Go earns its place — errgroup, semaphore, context |
| Schema + retention policy | Data model + cost control |
| `Checker` interface design | Go interfaces learning target |

**When asked about a 🟢 component:** explain, discuss trade-offs, review, point out bugs. Do not implement.

### 🟡 — assist, then explain

Lambda handler wiring, IAM policies, Cost Explorer integration, dashboard.
Include a short note on the alternative rejected and the reason.

### 🔴 — just do it

CRUD endpoints, migrations, config, n8n workflow, docs.

---

## Phase discipline

| Phase | What | Tier |
|---|---|---|
| 1 | Local Go binary: ticker, Neon, checks, state machine, retry | 🟢 MVP |
| 2 | Lambda + EventBridge + IAM | 🟡 |
| 3 | Flap suppression + incidents + SNS alerts + n8n | 🟡 |
| 4 | Cost tracking (verify §D9 first) | 🟡 🔵 |
| 5 | Next.js dashboard + Go API | 🟡 🔵 |
| 6 | Playwright journeys (optional) | 🔵 |

**Phase 1 is local Go only. No AWS, no Lambda, no EventBridge.** Do not introduce cloud infrastructure before Phase 2.

---

## Go conventions

- **No web framework.** stdlib `net/http` + `chi` for routing + `pgx` for Postgres. No Gin, no Echo, no GORM.
- All check types implement a `Checker` interface — don't collapse HTTP, TCP, and health checks into one function
- Explicit error handling everywhere — no `_` discarding errors unless intentionally ignored with a comment
- `context.Context` propagated through every function that does I/O
- `errgroup` + channel-as-semaphore for bounded concurrent fan-out
- Structs and struct tags for JSON — no `map[string]interface{}`
- `time.Duration` for timeouts, not raw integers
- No goroutine leaks — every goroutine has a clear exit condition

**On `if err != nil`:** this will feel tedious for about a week coming from JS/Python. Push through — that's the language, not you doing it wrong.

---

## The concurrency pattern (🟢 implement this yourself)

```go
g, ctx := errgroup.WithContext(ctx)
sem := make(chan struct{}, maxConcurrent)

for _, m := range monitors {
    m := m
    g.Go(func() error {
        select {
        case sem <- struct{}{}:
            defer func() { <-sem }()
        case <-ctx.Done():
            return ctx.Err()
        }
        cctx, cancel := context.WithTimeout(ctx, m.Timeout)
        defer cancel()
        return runCheck(cctx, m)
    })
}
err := g.Wait()
```

Understand every line before using it. This is idiomatic Go, not a copy-paste pattern.

---

## Alert discipline

- Alerts fire on **state transition**, never on state
- One alert when DOWN starts, one when it recovers
- Flap suppression: above N transitions in window → FLAPPING → one alert → suppress
- Never alert every minute a monitor is down — that's the failure mode

---

## Retention — from day one

`check_results` grows at one row per monitor per interval. Without a retention policy it becomes unmanageable quickly.

- Raw results: 30 days
- Hourly aggregates: indefinitely
- Implement the policy in Phase 1 before the table has a single row

---

## What to flag rather than fix

- A check that calls a provider or browser without a timeout
- An alert that would fire every cycle rather than on transition
- A decision in `DECISIONS.md` that a suggestion contradicts
- Cost Explorer usage per page load (must be daily snapshots only)
- Phase creep — a feature belonging to a later phase
- A goroutine without a clear exit condition

---

## Non-negotiable constraints (short form)

Full reasoning in `DECISIONS.md`:

- **No framework** — stdlib + `chi` only
- **Phase 1 is local** — no AWS until Phase 2
- **Alerts on transition, never on state**
- **Retry with backoff** before recording any failure
- **State machine** with N/M confirmation thresholds — no immediate transitions
- **Flap suppression** before alerting goes live (Phase 3)
- **Cost Explorer daily snapshots** — never per request
- **Retention policy** from the first migration
