# Sukko CLI Engineering Principles

These are the engineering principles that govern the Sukko CLI codebase —
adapted from the [Sukko platform engineering principles](https://github.com/sukko-dev/sukko/blob/main/docs/engineering-principles.md).
Code comments cite them by section: a comment like `// per §III` refers to
section III of this document. Sections V (Structured Logging) and VI
(Observability) are intentionally absent — the CLI does not use zerolog or
Prometheus — and a cited section that does not exist here (e.g. §XVIII)
refers to the platform document. Architecture decisions are recorded
separately in [`docs/adr/`](adr/); an `ADR-NNNN` reference not present there
refers to the [platform's ADR directory](https://github.com/sukko-dev/sukko/tree/main/docs/adr).

This is the published form of the project's internal engineering rules; the
two are kept in sync on every amendment. Some illustrative examples reference
platform services — the principles were adapted from the platform constitution
and share its vocabulary.

## I. Configuration

Every configurable parameter MUST be externalized via environment variables with `env:` struct tags. Go `envDefault:` values are the **single source of truth** for all configuration defaults — they MUST reflect production-intended values. Helm values and Docker Compose MUST NOT duplicate Go defaults; they override via env vars ONLY when a deployment requires a value different from the Go default (e.g., Kubernetes service discovery addresses, mode selections, resource-derived limits). Helm templates MUST NOT compute derived values — derived configuration MUST be computed in Go. Magic numbers MUST be named constants or configuration. Magic strings (URLs, broker addresses, topic names, tenant IDs, namespace prefixes) MUST NOT be hardcoded — they MUST come from configuration or be constructed from configured values. All configuration MUST be validated at startup with clear error messages. Hysteresis thresholds MUST enforce `lower < upper`. Enum values MUST be validated against allowed sets. Invalid configuration MUST cause immediate startup failure, not silent degradation. Configuration fields shared across multiple services (e.g., `Environment`, `LogLevel`, `LogFormat`) MUST be defined once in `platform.BaseConfig` and embedded by each service config — never duplicated across config structs. Shared and internal packages MUST NOT define their own defaults for deployment-level or runtime-critical settings (e.g., environment, namespace, tenant ID); they MUST receive these values from callers. Constructors and initializers that accept configuration from external or unvalidated sources MUST validate required fields and return an error when critical values are missing or empty — silent defaulting that could cause the system to run with wrong state is forbidden. Constructors that receive config structs already validated by the platform config layer (`Validate()` at startup) are exempt from re-validation; the config layer is the single validation boundary for deployment-level settings. CLI flags (e.g., `flag.Int`) MUST use layered defaults: load config from env vars first, then use the config value as the flag default — so the precedence is CLI flag > env var > `envDefault`. CLI flags MUST NOT define their own independent defaults; doing so creates a second source of truth that diverges from the env var config. Example: `flag.Int("shards", cfg.NumShards, "...")` where `cfg.NumShards` comes from `env:"WS_NUM_SHARDS" envDefault:"3"`.

## II. Defense in Depth

Every layer MUST validate its inputs. Never assume upstream validation. The gateway validates, and the server validates again. Input validation at ALL system boundaries is mandatory.

## III. Error Handling

All errors MUST be wrapped with context using `fmt.Errorf("operation: %w", err)`. Sentinel errors MUST be defined for expected conditions. Ignored errors MUST have an explicit comment explaining why. Silent failures are forbidden.

## IV. Graceful Degradation

Optional dependencies MUST use noop implementations or nil-guarded feature flags — never half-initialized state. Multi-step cleanup MUST continue on individual failures. Health endpoints MUST report degraded state. Retry logic MUST use exponential backoff with a cap. Slow WebSocket clients MUST be detected and disconnected via circuit breaker.

## VII. Concurrency Safety

This is a high-performance WebSocket server handling thousands of concurrent connections per pod. Incorrect concurrency primitives cause panics, deadlocks, goroutine leaks, and silent data corruption. Every concurrent pattern MUST follow the established patterns below.

**Design Preference** — Prefer goroutine ownership over shared memory with locks. When a piece of state needs concurrent access, the first choice SHOULD be a dedicated goroutine that owns the state and communicates via channels (Go proverb: "share memory by communicating"). Mutexes are acceptable for simple read-heavy caches (`sync.RWMutex`) and atomic counters, but for stateful operations (connection lifecycle, subscription tracking, auth flow), a single-owner goroutine with channel-based communication is safer and eliminates lock-ordering concerns.

**Goroutine Lifecycle** — All goroutines MUST be launched via `wg.Go()` (Go 1.25+), which handles `Add(1)` before launch and `Done()` after the function returns. The function passed to `wg.Go()` MUST follow this structure:
1. `defer logging.RecoverPanic(...)` MUST be the FIRST `defer` inside the function body.
2. The function MUST NOT call `wg.Done()` — `wg.Go()` calls it automatically. Calling `Done()` inside a `wg.Go()` function causes a double-Done, driving the WaitGroup counter negative and panicking at runtime.
3. The goroutine MUST check `ctx.Done()` in its main loop via `select` for shutdown signaling.
4. `wg.Wait()` MUST be called in the shutdown/stop path to ensure all goroutines have exited before resources are released.

**`wg.Go()` with inline closures** (preferred for short-lived or context-capturing goroutines):
```go
wg.Go(func() {
    defer logging.RecoverPanic(logger, "component_name", nil)
    doWork(ctx)
})
```

**`wg.Go()` with named methods** (preferred for long-lived goroutines with their own loops):
```go
wg.Go(s.runLoop)
// Inside runLoop: NO defer wg.Done() — wg.Go handles it.
func (s *Service) runLoop() {
    defer logging.RecoverPanic(s.logger, "runLoop", nil)
    for { select { case <-s.ctx.Done(): return } }
}
```

**CRITICAL — Modernization safety rule**: When converting legacy `wg.Add(1); go method()` patterns to `wg.Go(method)`, the `defer wg.Done()` inside the method body MUST be removed in the same change. Failing to do so causes double-Done. Both the call site and the method body MUST be updated atomically — never one without the other.

Shutdown ordering MUST be: cancel context → `wg.Wait()` for goroutines → close channels → release resources. Reversing this order (e.g., closing a channel before its goroutine exits) causes panics.

**Channels** — Channel type MUST match usage pattern:
- **Signal/stop channels** (`chan struct{}`): Used for shutdown signaling. MUST be closed by exactly one goroutine. If multiple goroutines may attempt close, MUST use `sync.Once` to guard the `close()` call. Sending on a closed channel panics — this is unrecoverable in production.
- **Data channels** (e.g., `chan OutgoingMsg`): MUST be buffered with a size matching throughput requirements. Unbuffered channels MUST NOT be used in hot paths (message distribution, broadcast fan-out) because a single slow receiver blocks all senders.
- **Semaphore channels** (`chan struct{}` with capacity = limit): Used for resource limiting (max connections, max goroutines). Acquire MUST be non-blocking (`select` with `default` case) to reject callers at capacity rather than queueing them indefinitely.
- **Fan-out sends** (broadcast to multiple subscribers): MUST use non-blocking `select` with `default` to skip slow consumers. Dropped messages MUST be counted via Prometheus metrics (`_dropped_total`). A single slow subscriber MUST NOT block delivery to all other subscribers.
- **Channel close rules**: Only the sender side MUST close a channel — never the receiver. After closing, no further sends are permitted (panic). When a channel may be closed from multiple code paths, guard with `sync.Once`. When draining a channel before reuse (e.g., `sync.Pool`), use `select` with `default` in a loop.

**WaitGroups** — `sync.WaitGroup` is for goroutine lifecycle tracking only. `wg.Go(func())` is the ONLY permitted launch pattern — manual `wg.Add(1); go func() { defer wg.Done(); ... }()` is legacy and MUST NOT be introduced in new code. Additional constraints:
- `wg.Wait()` SHOULD have a timeout mechanism (e.g., wrapper with `context.WithTimeout`) to detect stuck goroutines during shutdown rather than hanging indefinitely.
- WaitGroups MUST NOT be reused after `Wait()` returns for a given set of goroutines.
- Functions passed to `wg.Go()` MUST NOT call `wg.Done()` — this is the single most common modernization bug and causes a runtime panic.

**Mutexes** — Locks MUST protect data, not code:
- `sync.RWMutex` MUST be used for read-heavy data (caches, subscription maps, metrics snapshots) where reads vastly outnumber writes. `sync.Mutex` MUST be used only when writes are as frequent as reads.
- Critical sections MUST be minimal: lock → read/write shared data → unlock. Mutexes MUST NOT be held across I/O operations (network calls, disk reads, channel sends, HTTP requests). Holding a lock across I/O blocks all other goroutines waiting for that lock, destroying throughput.
- `defer mu.Unlock()` / `defer mu.RUnlock()` MUST be used to prevent deadlocks from early returns or panics. Inline `Unlock()` without defer is forbidden.
- Nested mutex acquisition (locking mutex A while holding mutex B) MUST follow a consistent global ordering to prevent deadlocks. If ordering cannot be guaranteed, restructure to avoid nesting.
- Mutex values MUST NOT be copied. Structs containing a mutex MUST be passed by pointer and MUST NOT be assigned by value.

**Atomics** — Lock-free operations for hot-path counters and flags:
- `atomic.Int64` MUST be used for hot-path counters (messages sent/received, bytes, connection counts) instead of mutex-protected `int64`. Lock contention on frequently-incremented counters degrades throughput under load.
- `atomic.Bool` MUST be used for status flags read frequently in hot paths (health status, circuit breaker state, shutdown flag).
- `atomic.Value` SHOULD be used for periodic snapshot caching (e.g., subscriber lists rebuilt on subscription change, read lock-free on every broadcast). `Store()` replaces the snapshot; readers use `Load()` with zero contention.

**sync.Pool** — `sync.Pool` MUST be used for frequent allocations in hot paths (per-connection `Client` objects, message buffers). Objects retrieved via `Get()` MUST be fully reset before reuse: drain all channels (non-blocking `select` loop), clear all maps, zero all fields. Returning a partially-reset object causes state leakage between connections.

**sync.Once** — `sync.Once` MUST be used when an operation must execute exactly once across concurrent goroutines: connection close (`net.Conn.Close()`) and singleton initialization. Calling `Close()` twice on a `net.Conn` panics — `sync.Once` prevents this. Channel close guarding is covered in Channels close rules above.

**Message Pipeline Protection** — The message delivery pipeline (ingestion → broadcast bus → shard fan-out → per-client write pump → WebSocket write) is the critical hot path. Feature-level operations (auth refresh, subscription management, metrics collection, provisioning lookups, OIDC validation) MUST NOT introduce blocking on this path. Specifically:
- Locks acquired for feature operations MUST NOT be held while calling into the pipeline (`forwardFrame`, `sendToClient`, `bus.Publish`, write pump sends).
- Feature operations that run on the client→backend goroutine MUST complete without waiting for backend responses when the wait would stall message reads from the client.
- Backend→client forwarding MUST remain non-blocking: observational interception (subscription tracking, metrics) MUST NOT add latency that degrades broadcast throughput.

## VIII. Testing

Tests MUST be run with Go's race detector (`-race` flag) in local development and CI. The race detector is the primary automated enforcement mechanism for VII (Concurrency Safety). Test runs without `-race` MUST NOT be considered passing. Tests MUST be table-driven for multiple cases. Mocks MUST use interfaces. `t.Parallel()` MUST NOT be used on tests with shared resources (databases, external services, `*_shared_test.go`). Edge cases MUST be covered (empty, nil, max values, error paths).

**Test coverage for all changes is mandatory:**
- **Bug fixes** MUST update or add unit tests that reproduce the bug and verify the fix. If existing tests missed the bug, they MUST be strengthened.
- **Enhancements and refactors** MUST update existing tests to reflect the changed behavior and add tests for any new code paths introduced.
- **New features** MUST include comprehensive unit tests covering: happy path, error paths, edge cases, and concurrency safety (where applicable).
- No code change (bug fix, enhancement, refactor, or feature) MUST be considered complete without corresponding test updates. Untested code changes are forbidden.

## IX. Security

Rate limiting MUST be applied at multiple levels (global, per-IP, per-tenant). Secrets MUST never appear in logs or error messages. JWT validation MUST verify expiration and issuer. `//nolint` or `#nosec` MUST include thorough written justification. Input validation at boundaries is mandated by II.

## X. Shared Code Consolidation

Before writing any new utility, `internal/shared/` MUST be checked for existing implementations. Duplicate functions, error definitions, constants, and types across packages are forbidden. HTTP utilities MUST use `shared/httputil/`. Auth helpers MUST use `shared/auth/`. New shared code MUST have tests. Conversely, types, functions, constants, interfaces, and structs used by only one service MUST live in that service's package — never in `internal/shared/`, even if they serve a similar purpose to shared types. The shared package is exclusively for code referenced by multiple services. Service-specific code in shared violates separation of concern and creates false coupling.

## XI. Prior Art Research

Before designing any new feature or protocol extension, the implementation approach MUST be informed by how established real-time/WebSocket services have solved the same problem. Reference services: Pusher Channels, Ably, Socket.IO, Phoenix Channels, Centrifugo, PubNub. Research MUST identify: (1) the common industry pattern for the feature, (2) edge cases and failure modes that mature implementations handle, (3) where Sukko's architecture requires deviation from the common pattern — with documented rationale for the deviation. "Not invented here" solutions to solved problems are forbidden.

## XII. API Design

**REST** — All external-facing APIs (admin, CLI, third-party) MUST use REST over HTTP/JSON.
- Endpoints MUST be versioned via URL path (`/api/v1/`). Health, readiness, and metrics endpoints MUST be at root level (no version).
- Routes MUST be resource-oriented: `POST` (create, 201), `GET` (read, 200), `PATCH` (partial update, 200), `PUT` (full replace, 200), `DELETE` (remove, 200). State-change actions use `POST` on sub-resources (`/suspend`, `/reactivate`).
- Error responses MUST use the `httputil.ErrorResponse` format: `{"code": "UPPER_SNAKE_CASE", "message": "human-readable"}`. HTTP status codes MUST map to semantics: 400 validation, 401 authn, 403 authz, 404 not found, 409 conflict, 500 internal.
- List endpoints MUST support pagination (`?limit=N&offset=M`) with defaults and max caps. Responses: `{items, total, limit, offset}`.
- All response writing MUST use `shared/httputil/` helpers (`WriteJSON`, `WriteError`). Raw `w.Write()` in handlers is forbidden.

**gRPC** — All internal service-to-service communication MUST use gRPC with protobuf.
- Proto files MUST live in `ws/proto/` with package naming `sukko.{service}.v1`. Style: `PascalCase` messages/services, `snake_case` fields, `UPPER_SNAKE_CASE` enums. Code generation via `buf generate`; generated code committed to repo. `buf lint` MUST pass in CI.
- Server-side streaming MUST be used for real-time data push (watch/subscribe). Unary RPCs for request-response.
- gRPC status codes MUST map to domain semantics: `NotFound`, `InvalidArgument`, `FailedPrecondition` (state conflict), `Internal`, `Unavailable` (temporary). Context via `status.Errorf()`.
- gRPC servers MUST run on a dedicated port, separate from HTTP. Both listeners MUST support graceful shutdown.
- Interceptors MUST handle: panic recovery (first), structured logging, Prometheus metrics (latency histograms, call counters).
- Stream clients MUST reconnect with exponential backoff and jitter, serve stale cache during disconnection, and reflect stream health in service health endpoints.

## XIII. Decision Records

Durable engineering decisions are recorded as Architecture Decision Records in
[`docs/adr/`](adr/) — any choice that is likely to be challenged, expensive to reverse, or
needed by a future contributor MUST be recorded at the moment it is made. Accepted ADRs are
never edited — they are superseded by new ones. ADRs capture the decision, its context and
consequences, and the rejected alternatives.

Planning artifacts are ephemeral — there are no per-feature specification or plan documents
in the repository; the durable outputs of design work are ADRs and committed documentation.

## Governance

- These principles supersede ad-hoc practice in the codebase.
- **Correctness over pattern**: When existing code is incorrect — whether it violates these principles, Go best practices, robustness principles, testability, Go idioms, readability, security, or performance — correctness wins. NEVER replicate a broken pattern just because the codebase uses it. Broken code is not precedent; it is a bug. Code MUST be evaluated against: (1) these principles, (2) Go best practices and idioms, (3) robustness and error handling correctness, (4) testability, (5) readability, (6) security, (7) performance. If a pre-existing deficiency is discovered during a change, fix both the new code and the pre-existing deficiency.
- All code changes MUST verify compliance with these principles.
