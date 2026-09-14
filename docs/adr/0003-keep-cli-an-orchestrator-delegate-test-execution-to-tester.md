# ADR-0003: Keep the CLI an orchestrator and delegate test execution to the tester

**Status**: Accepted
**Date**: 2026-03-31
**Ticket**: feat/cli-tester-boundary

## Context

The platform ships two operator tools: the `sukko` CLI (user-facing) and the sukko-tester service (a headless REST-driven test engine supporting connections/load/validation/metrics with SSE streaming). Their responsibilities were blurring — operators had to configure the tester's environment variables separately from the CLI's context, defeating context management. Both repositories needed one canonical, shared boundary so neither reimplements the other. A concrete coupling problem also had to be resolved: the CLI's context URLs are host-facing (`localhost`), but the tester runs inside Docker and needs internal service URLs.

## Decision

Adopt a formal boundary contract, documented in this repo so both repos share it: **the CLI is the orchestrator; the tester is the execution engine.** The CLI owns user interaction, context/secret management, local Docker Compose lifecycle, license handling, platform-state CRUD, interactive subscribe/publish, and test *orchestration* (trigger, stream, format). The tester owns test *execution*: load generation, concurrent connections, validation logic, delivery/sequence verification, JWT/keypair minting per run, and metrics. The CLI never executes tests, mints test JWTs, or duplicates tester state; the tester never manages platform state, knows about Docker Compose, or stores secrets persistently. `sukko test *` delegates to the tester via `POST /api/v1/tests`, passing an **all-or-nothing** `context` block (never partial). Context passthrough is applied by mode: **remote** deployments send the full block; **localhost** deployments send no block so the tester uses its own in-Docker env URLs; incomplete context sends no block and the tester falls back to its env vars.

## Consequences

- Operators run remote tests with zero separate tester configuration when the CLI context is complete.
- The identity of each tool is explicit, giving contributors a clear rule for where new logic belongs and preventing the CLI from growing a duplicate test engine (expensive to unwind later).
- The all-or-nothing context rule forces the CLI to validate completeness before sending, avoiding half-configured test runs.
- The localhost-vs-remote split hardcodes a URL heuristic (`localhost`/`127.0.0.1`) as the signal for which side owns connection URLs.
- The tester must remain independently operable via REST + env vars, so it cannot take a hard dependency on the CLI.

## Alternatives rejected

- **Execute tests inside the CLI** — would duplicate the concurrency-optimized engine and its metrics/verification, and bloat the user-facing tool.
- **Always send the CLI context block (including localhost)** — host URLs are unreachable from inside the tester's container; local dev must use the tester's internal env URLs.
- **Send partial context blocks** — ambiguous merge with tester env vars; all-or-nothing is unambiguous.
