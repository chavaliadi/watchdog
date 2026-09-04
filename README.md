# Deployment Watchdog

**Are my projects still alive, and what are they costing me?**

Eleven deployed projects. No idea which are still running. A recruiter clicks a portfolio link and gets a 404. A free tier expired quietly. A stray instance has been billing since March.

The Watchdog checks whether your things are alive, tracks costs, and tells you when something breaks — before the recruiter or the invoice does.

---

## Deliberately not AI

This project proves you can build useful software without an LLM. AI stays out of the core entirely. Optional tiny feature, much later: summarise an incident in plain English. That's the ceiling.

---

## Stack

| Layer | Choice |
|---|---|
| Backend | Go — stdlib + `chi` + `pgx` |
| DB | Neon Postgres → RDS later |
| Compute | AWS Lambda (`provided.al2023`) |
| Scheduler | EventBridge |
| Logs / metrics | CloudWatch |
| Alerts | SNS → n8n |
| Permissions | IAM |
| Frontend | Next.js + strict TypeScript + Tailwind (Phase 5) |
| Browser checks | Playwright + hosted browser (Phase 6, optional) |

No framework. stdlib `net/http` + `chi` only. No Gin, no Echo.

## Portfolio story

**Cloud Engineering + Go** — goroutines, context, interfaces, explicit error handling, AWS serverless.

---

## Current phase

**Running alongside P1 as a break project.**

Phase 1 is local Go only — no AWS. Build the state machine and concurrency right on your machine before touching Lambda.

---

## Docs

| File | What it covers |
|---|---|
| `docs/PROJECT_PLAN.md` | MVP scope, phases, schema, system design, risks |
| `docs/DECISIONS.md` | Every architectural decision + rejected alternative |
| `docs/DEVELOPMENT_RULES.md` | Build split, Go conventions, what agent does / doesn't write |

---

## Rules for the coding agent

- Read `docs/PROJECT_PLAN.md` before implementing any new component
- Sections marked 🟢 are written by the human — explain and review, do not implement
- Phase 1 is local Go with no AWS — do not introduce Lambda or EventBridge before Phase 2
- No framework — stdlib + `chi` only
- Alerts fire on state **transition**, never on state — never alert every minute a monitor is down
- When a suggestion conflicts with `docs/DECISIONS.md`, surface the conflict
