# Sukko CLI

Command-line interface for the [Sukko](https://github.com/sukko-dev/sukko) WebSocket infrastructure platform.

The CLI serves two purposes:

- **Local development** — spin up the full Sukko platform locally via Docker Compose (`sukko init` + `sukko up`), with optional observability stack (Prometheus, Grafana, Tempo, Pyroscope)
- **Remote operations** — manage tenants, keys, rules, and inspect any deployed Sukko instance by switching contexts (`sukko context use staging`)

## Install

### Homebrew (macOS/Linux)

```bash
brew install sukko-dev/tap/sukko
```

### Scoop (Windows)

```powershell
scoop bucket add sukko https://github.com/sukko-dev/scoop-bucket
scoop install sukko
```

### Go

```bash
go install github.com/sukko-dev/cli@latest
```

### Binary

Download from [Releases](https://github.com/sukko-dev/cli/releases). Available for Linux, macOS, and Windows on amd64 and arm64.

## Quick Start

```bash
# Initialize a local context (stores server URLs and credentials)
sukko init

# Start local development stack (Docker Compose)
sukko up

# Check edition and resource usage
sukko edition

# Create a tenant
sukko tenant create --slug my-app --name "My App"

# Generate an ES256 key pair and register it
sukko keys create --tenant my-app --generate

# Generate a JWT token (auto-discovers stored key)
sukko token generate --tenant my-app --sub user1

# Subscribe to a channel
sukko subscribe orders.new --token <jwt>

# Publish a message (in another terminal)
sukko publish orders.new '{"id": "123", "amount": 99.99}' --token <jwt>

# Stop local stack
sukko down
```

## Authentication

The CLI supports two authentication models:

### Admin Authentication (Provisioning API)

Ed25519 keypair-based JWT authentication for managing tenants, keys, rules, and quotas.

```bash
# Generate an Ed25519 admin keypair
sukko auth keygen

# Register the public key with provisioning
sukko auth register

# All admin commands now auto-sign requests with 5-minute JWTs
sukko tenant list
```

Admin keys are stored per-context. For Kubernetes bootstrap, `sukko auth keygen` outputs the base64 public key for Helm values.

### Gateway Authentication (WebSocket/SSE)

Two methods for connecting to the gateway:

```bash
# API key auth (simpler, for public channels)
sukko keys create --tenant my-app --generate
sukko subscribe chat --api-key <key>

# JWT auth (full auth path with user identity, roles, groups)
sukko keys create --tenant my-app --generate        # generate + register ES256 key pair
sukko token generate --tenant my-app --sub user1   # auto-discovers stored key
sukko subscribe chat --token <jwt>
```

JWT tokens support claims for fine-grained access control:

```bash
sukko token generate \
  --tenant my-app \
  --sub user1 \
  --roles admin,moderator \
  --groups vip,traders \
  --scopes read:orders,write:orders \
  --ttl 24h
```

## Contexts

Contexts store per-environment configuration (URLs, encrypted credentials, license keys). All secrets are encrypted at rest with XChaCha20-Poly1305.

```bash
# Create a local dev context (interactive setup)
sukko init

# Create a remote context (interactive, secrets masked)
sukko context create staging

# List all contexts
sukko context list

# Switch between environments
sukko context use staging

# Show active context details
sukko context current

# Remove a context
sukko context remove old-env
```

Each context stores:
- Gateway URL (WebSocket endpoint)
- Provisioning URL (admin API)
- Tester URL (optional)
- Admin token or keypair (encrypted)
- API key (encrypted)
- License key (encrypted)
- Environment name
- Default tenant

Non-interactive context creation via flags:

```bash
sukko context create production \
  --provisioning-url https://api.sukko.example.com \
  --gateway-url wss://ws.sukko.example.com \
  --admin-token <token> \
  --environment production \
  --tenant acme
```

**Resolution order**: CLI flag > environment variable > context value > default.

## Local Development

### Initialization

`sukko init` creates a `.sukko/config.json` with infrastructure selections and a `local` context with dev credentials.

```bash
# Interactive setup (prompts for each choice)
sukko init

# Skip prompts and use defaults (postgres + Valkey + direct)
sukko init --defaults
```

Infrastructure choices:

| Component | Options | Default |
|-----------|---------|---------|
| Message backend | `direct`, `kafka`, `redpanda` | `direct` |
| Observability | `yes`, `no` | `no` |
| Distributed tracing | `yes`, `no` | `yes` (if observability enabled) |
| Continuous profiling | `yes`, `no` | `yes` (if observability enabled) |

The broadcast bus (Valkey) and database (Postgres) are fixed — `init` does not prompt for them.

### Starting & Stopping

```bash
# Start all services (reads .sukko/config.json)
sukko up

# Pull latest images before starting
sukko up --pull

# Stop services
sukko down

# Stop and remove all volumes (full reset)
sukko down -v
```

`sukko up` activates Docker Compose profiles based on your selections, waits for all services to become healthy, and provisions a default `demo` tenant with routing and channel rules.

### Services

| Service | URL | Description |
|---------|-----|-------------|
| Provisioning | `http://localhost:18080` | Admin API |
| Gateway | `ws://localhost:13000` | WebSocket + SSE + REST publish |
| WS Server | `http://localhost:3005` | Message routing server |
| Tester | `http://localhost:8090` | Test orchestration |
| Grafana | `http://localhost:3030` | Dashboards (if observability enabled) |
| Prometheus | `http://localhost:19091` | Metrics (if observability enabled) |
| AlertManager | `http://localhost:9093` | Alerting (if observability enabled) |

### Logs

```bash
# All services
sukko logs -f

# Specific services
sukko logs ws-gateway ws-server -f
```

### Observability

When enabled during `sukko init`, the stack includes Prometheus, Grafana, Tempo (distributed tracing), Pyroscope (continuous profiling), and AlertManager.

```bash
# Open Grafana in the browser
sukko grafana
```

## Tenant Management

```bash
# Create a tenant
sukko tenant create --slug acme --name "ACME Corp" --consumer-type shared

# List tenants
sukko tenant list
sukko tenant list --limit 100 --offset 0 --status active

# Get tenant details (uses active tenant from context if no ID given)
sukko tenant get
sukko tenant get acme

# Update a tenant
sukko tenant update acme --name "ACME Corporation"

# Lifecycle management
sukko tenant suspend acme
sukko tenant reactivate acme
sukko tenant deprovision acme    # initiates deletion with grace period (alias: sukko tenant delete)
```

## JWT Signing Keys

Manage per-tenant ES256/RS256/EdDSA signing keys for JWT authentication.

```bash
# Auto-generate an ES256 key pair (registers public key, saves private key locally)
sukko keys create --tenant acme --generate

# Manual: register an existing public key
sukko keys create --tenant acme --algorithm ES256 --public-key-file key.pub

# Optional on create: --key-id <id> (auto-generated if omitted), --expires-at <RFC3339>

# List keys
sukko keys list --tenant acme

# Revoke a key
sukko keys revoke --tenant acme --key-id <key-id>
```

Generated private keys are stored in `~/.config/sukko/keys/<tenant-id>/<key-id>.pem`.

## API Keys

API keys provide simple authentication for gateway access (WebSocket, SSE, REST publish).

```bash
# Create an API key
sukko api-keys create --tenant acme --name "web-app"

# List API keys
sukko api-keys list --tenant acme

# Revoke an API key
sukko api-keys revoke --tenant acme --key-id <key-id>
```

## Webhooks

Webhook management is an operator action (Pro/Enterprise) — it authenticates with your admin keypair, like keys and routing rules. Webhooks deliver channel events to an external HTTPS endpoint, HMAC-signed with a shared secret.

```bash
# Register a webhook
sukko webhook create --tenant acme --url https://acme.example.com/hook \
  --channel-pattern "orders.*" --secret "$WEBHOOK_SECRET" --max-retries 5

# List / inspect
sukko webhook list --tenant acme
sukko webhook get --tenant acme --webhook-id <webhook-id>

# Update (only the flags you set change)
sukko webhook update --tenant acme --webhook-id <webhook-id> --status suspended

# Send a one-off test delivery
sukko webhook test --tenant acme --webhook-id <webhook-id>

# Delete
sukko webhook delete --tenant acme --webhook-id <webhook-id>
```

## Token Generation & Validation

```bash
# Generate a JWT (auto-discovers stored key for tenant)
sukko token generate --tenant acme --sub user1

# Generate with full claims
sukko token generate \
  --tenant acme \
  --sub user1 \
  --roles admin,moderator \
  --groups vip \
  --scopes read:orders,write:orders \
  --ttl 24h

# Generate with explicit key file and algorithm
sukko token generate --tenant acme --sub user1 --algorithm ES256 --key-file private.pem

# Decode and validate a token
sukko token validate <token>

# Validate with signature verification
sukko token validate <token> --key-file public.pem

# Revoke a specific session by jti
sukko token revoke --jti <session-id> --tenant acme

# Revoke all sessions for a user
sukko token revoke --sub user@example.com --tenant acme

# Revoke by passing the JWT directly (extracts jti and tenant automatically)
sukko token revoke --token <jwt>

# Revoke with custom expiry (duration or RFC3339)
sukko token revoke --jti <id> --tenant acme --expires 2h
```

## Channel Rules

### Routing Rules

Control how messages are routed to Kafka topics.

```bash
# Get routing rules
sukko rules routing get --tenant acme

# Set from file
sukko rules routing set --tenant acme --file routing.json

# Add a single routing rule (--pattern, --ingress-topic, --priority required)
sukko rules routing add --tenant acme --pattern "orders.**" --ingress-topic orders --priority 50

# Delete routing rules
sukko rules routing delete --tenant acme
```

Example `routing.json` (rules are evaluated in ascending priority order — lower value matches first):

```json
{
  "rules": [
    {"pattern": "orders.**", "ingress_topic": "orders", "priority": 50},
    {"pattern": "**",        "ingress_topic": "default", "priority": 100}
  ]
}
```

### Topics

Manage the tenant's provisioned topics. The deterministic `default` topic is
always available and is listed first; it and `dead-letter` are reserved and
cannot be created or deleted. The topic count is bounded by the tenant's
`max_topics` quota (see `sukko quota`).

```bash
# List provisioned topics
sukko topics list --tenant acme

# Create a topic (suffix: lowercase alphanumeric and hyphens)
sukko topics create --tenant acme --suffix analytics

# Delete a topic — rejected if a routing rule still references it
# (TOPIC_REFERENCED_BY_RULE); remove or update the rule first.
sukko topics delete --tenant acme --suffix analytics
```

### Channel Permission Rules

Control which channels are accessible (public, private, user-scoped, group-scoped).

```bash
# Set public channel patterns
sukko rules channels set --tenant acme --public "*" --public "chat.*"

# Set from file (for complex rules)
sukko rules channels set --tenant acme --file channels.json

# Get channel rules
sukko rules channels get --tenant acme

# Delete channel rules
sukko rules channels delete --tenant acme

# Test access for a subject
sukko rules test-access --tenant acme --sub user1 --channel orders.new --group vip
```

## Quotas

```bash
# Get quotas
sukko quota get --tenant acme

# Update quotas
sukko quota update --tenant acme \
  --max-connections 1000 \
  --max-topics 50 \
  --max-partitions 100 \
  --max-storage-bytes 10737418240 \
  --producer-byte-rate 1048576 \
  --consumer-byte-rate 2097152
```

## WebSocket Operations

### Subscribe

```bash
# Subscribe to one or more channels
sukko subscribe orders.new customer.updates --token <jwt>

# Subscribe with API key
sukko subscribe chat --api-key <key>

# Subscribe using context credentials
sukko subscribe orders.*
```

Messages stream to stdout. Press Ctrl+C to disconnect — a summary of total messages, duration, and channels is printed on exit.

### Publish

```bash
# Publish a single message (must be valid JSON)
sukko publish orders.new '{"id": "123", "amount": 99.99}' --token <jwt>

# Publish multiple messages with interval
sukko publish metrics '{"value": 42}' --count 10 --interval 1s

# Publish with API key
sukko publish chat '{"text": "hello"}' --api-key <key>
```

### Connection Test

Quick WebSocket connectivity check without the tester service:

```bash
sukko connections test
sukko connections test --gateway-url ws://staging.example.com --token <jwt> --timeout 5s
```

Tests WebSocket upgrade (101), subscribe, and subscription acknowledgment. Reports pass/fail with latency.

## Testing

The built-in tester service (included in Sukko deployments) runs smoke, load, stress, soak, and validation tests. The CLI delegates test execution to the tester via its REST API and streams results via SSE.

### Test Types

```bash
# Quick connectivity and health check
sukko test smoke -f

# Sustained load test
sukko test load --connections 100 --duration 5m --publish-rate 10 -f

# Push to maximum capacity
sukko test stress --connections 1000 --ramp-rate 100 --duration 2m -f

# Long-running stability test
sukko test soak --connections 50 --duration 2h -f

# Run a validation suite
sukko test validate --suite auth -f
```

### Validation Suites

Suites are dynamically fetched from the tester. Tab completion works when the tester is running.

| Suite | Description |
|-------|-------------|
| `auth` | JWT authentication validation (valid, expired, wrong kid, wrong tenant, revoked, missing) |
| `channels` | Channel subscribe/unsubscribe operations |
| `ordering` | Message FIFO ordering verification |
| `reconnect` | Disconnect/reconnect session recovery |
| `ratelimit` | Rate limit enforcement detection |
| `edition-limits` | Edition boundary limit testing |
| `pubsub` | Pub-sub delivery with channel scoping (public, user, group) |
| `tenant-isolation` | Cross-tenant message isolation |
| `provisioning` | Provisioning API validation |
| `sse` | SSE transport delivery (Pro+) |
| `rest-publish` | REST publish endpoint delivery |
| `push` | Web Push / FCM / APNs registration and delivery (Pro+ for Web Push; FCM/APNs delivery needs Enterprise) |
| `license-reload` | License hot-reload behavior |
| `token-revocation` | Token/session revocation enforcement |
| `kafka-ingest` | Direct-to-Kafka ingestion (requires `--kafka-brokers`) |

The tester exposes additional suites depending on configuration (e.g. `api-key`, `upgrade`, and `webhooks` on Pro+). The `stress` and `soak` load tests also accept `--suite` (e.g. `--suite revocation`, Pro+). Run `sukko test validate --help` (with the tester running) for the live list.

```bash
# Run the kafka-ingest suite (requires a Kafka/Redpanda backend)
sukko test validate --suite kafka-ingest --kafka-brokers <broker-host:port> --kafka-topic-namespace <namespace> -f

# Override tester URL
sukko test smoke --tester-url http://tester.staging:8090 -f

# Target a specific tenant (omit to use an ephemeral throwaway tenant)
sukko test validate --suite auth --tenant acme -f
```

### Context Passthrough

For remote contexts, the CLI automatically passes gateway URL, provisioning URL, admin credentials, and environment to the tester. For local contexts (localhost), the tester uses Docker-internal URLs.

## Edition & Licensing

### Edition Info

```bash
# Show current edition, limits, and live resource usage
sukko edition

# Compare all editions side-by-side
sukko edition compare
```

`sukko edition` fetches live data from provisioning and gateway, showing current usage against limits with color-coded percentages (green < 75%, yellow 75-90%, red > 90%). Falls back to the locally stored license key if the platform is unreachable.

### Edition Comparison

The full data path — Kafka/Redpanda backend, message history, live gap recovery, REST publish, and channel-to-topic routing — is available on every edition; the capacity limits are the tier wall.

| Dimension | Community | Pro | Enterprise |
|-----------|-----------|-----|------------|
| Tenants | 3 | 50 | Unlimited |
| Total Connections | 500 | 10,000 | Unlimited |
| Shards | 1 | 8 | Unlimited |
| Topics/Tenant | 10 | 50 | Unlimited |
| Routing Rules/Tenant | 10 | 100 | Unlimited |
| Kafka Backend | Yes | Yes | Yes |
| Message History | Yes | Yes | Yes |
| Live Gap Recovery | Yes | Yes | Yes |
| REST Publish | Yes | Yes | Yes |
| Channel-Topic Routing | - | Yes | Yes |
| Tenant Limits & Quotas | - | Yes | Yes |
| Alerting | - | Yes | Yes |
| SSE Transport | - | Yes | Yes |
| Webhooks | - | Yes | Yes |
| Admin UI | - | Yes | Yes |
| Web Push | - | Yes | Yes |
| Push Analytics | - | Yes | Yes |
| Mobile Push (FCM/APNs) | - | - | Yes |
| Audit Logging | - | - | Yes |

### License Management

```bash
# Store a license key (prompts for input to avoid shell history)
sukko license set

# Store via argument
sukko license set <key>

# View stored license info (key masked)
sukko license show

# Remove stored license
sukko license remove
```

License keys are encrypted at rest. The key is automatically passed to services on `sukko up`.

### Push Notifications Upgrade

Push a Pro or Enterprise license to a running deployment to enable push notifications. Web Push (VAPID) is available from Pro; FCM/APNs delivery additionally requires Enterprise:

```bash
# Push a Pro or Enterprise license — push-service starts automatically (local dev)
sukko license push <pro-or-enterprise-key>

# Downgrade below Pro — push-service stops automatically
sukko license push <community-key>
```

**Requirements**: Push-service requires `kafka` or `redpanda` message backend. If your backend is `direct`, run `sukko init` to reconfigure before upgrading (the license still applies; only push-service orchestration is skipped).

**Fresh start**: If `SUKKO_LICENSE_KEY` is set to a Pro or Enterprise key, `sukko up` automatically starts push-service alongside core services.

**Kubernetes**: After pushing the license, enable push-service via Helm:
```bash
helm upgrade sukko deployments/helm/sukko --set push-service.enabled=true
```

## Health & Status

```bash
# Check health of all components with troubleshooting tips
sukko health

# Show unified service status
sukko status
```

`sukko health` probes provisioning, gateway, server, and tester health endpoints, reporting per-component status with troubleshooting suggestions on failure.

`sukko status` uses Docker Compose for local environments and HTTP health checks for remote.

## Configuration

### Global Flags

| Flag | Environment Variable | Default | Description |
|------|---------------------|---------|-------------|
| `--api-url` | `SUKKO_API_URL` | `http://localhost:18080` | Provisioning API URL |
| `--token` | `SUKKO_TOKEN` | — | Admin auth token |
| `--context` | `SUKKO_CONTEXT` | — | Active context name |
| `-o, --output` | — | `table` | Output format (`table` or `json`) |
| `--verbose` | — | `false` | Enable verbose output |

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SUKKO_API_URL` | `http://localhost:18080` | Provisioning API URL |
| `SUKKO_GATEWAY_URL` | `ws://localhost:13000` | WebSocket gateway URL |
| `SUKKO_GATEWAY_HTTP_URL` | `http://localhost:13000` | Gateway HTTP endpoint |
| `SUKKO_SERVER_URL` | `http://localhost:3005` | WebSocket server URL |
| `SUKKO_TESTER_URL` | `http://localhost:8090` | Tester service URL |
| `SUKKO_TOKEN` | — | Admin auth token |
| `SUKKO_TESTER_TOKEN` | — | Tester API auth token |
| `SUKKO_DEV_ADMIN_TOKEN` | — | Admin token for the local dev context (read by `sukko init`) |
| `SUKKO_LICENSE_KEY` | — | License key passthrough |
| `SUKKO_CONTEXT` | — | Active context name |

### View Active Configuration

```bash
# Show all env var defaults (table, env, or json format)
sukko config defaults
sukko config defaults --format env
sukko config defaults --format json

# Fetch live configuration from provisioning
sukko config view
```

## Shell Completion

```bash
# Bash
source <(sukko completion bash)
echo 'source <(sukko completion bash)' >> ~/.bashrc

# Zsh
source <(sukko completion zsh)
echo 'source <(sukko completion zsh)' >> ~/.zshrc

# Fish
sukko completion fish | source
sukko completion fish > ~/.config/fish/completions/sukko.fish
```

## Command Reference

### Works everywhere (local + remote)

| Command | Description |
|---------|-------------|
| `sukko auth` | Manage admin Ed25519 keypairs (keygen, register, revoke, list) |
| `sukko tenant` | Manage tenants (create, get, list, update, suspend, reactivate, deprovision) |
| `sukko keys` | Manage JWT signing keys (`--generate` for ES256 key pair) |
| `sukko api-keys` | Manage API keys for gateway access |
| `sukko token` | Generate, validate, and revoke JWT tokens |
| `sukko subscribe` | Subscribe to WebSocket channels and stream messages |
| `sukko publish` | Publish messages to channels |
| `sukko connections test` | Quick WebSocket connectivity check |
| `sukko rules` | Manage routing and channel permission rules |
| `sukko quota` | Manage tenant quotas |
| `sukko edition` | Show current edition, limits, and resource usage |
| `sukko edition compare` | Compare Community, Pro, and Enterprise editions |
| `sukko license` | Store, view, and remove license keys |
| `sukko health` | Check service health with troubleshooting tips |
| `sukko status` | Show platform status |
| `sukko test` | Run smoke, load, stress, soak, and validation tests |
| `sukko context` | Create, add, switch, list, or remove contexts |
| `sukko config` | View config defaults and live configuration |
| `sukko completion` | Generate shell completions (bash, zsh, fish) |
| `sukko version` | Print version info |

### Local development only (Docker Compose)

| Command | Description |
|---------|-------------|
| `sukko init` | Set up local context + infrastructure selections |
| `sukko up` | Start local dev environment (activates selected profiles) |
| `sukko up --pull` | Pull latest images before starting |
| `sukko down` | Stop local dev environment (`-v` to remove volumes) |
| `sukko logs` | View Docker Compose service logs (`-f` to follow) |
| `sukko grafana` | Open Grafana dashboard in browser |

## Engineering

- [Engineering principles](docs/engineering-principles.md) — the rules this
  codebase is built and reviewed against. Code comments cite them by section:
  `// per §III` refers to section III of that document; a section that does not
  exist there (e.g. §XVIII) refers to the
  [platform principles](https://github.com/sukko-dev/sukko/blob/main/docs/engineering-principles.md).
- [Architecture decision records](docs/adr/) — durable decisions with context
  and rejected alternatives; comments cite them as `ADR-NNNN`. A number not
  present locally refers to the
  [platform's ADR directory](https://github.com/sukko-dev/sukko/tree/main/docs/adr).

## License

Apache License 2.0 — see [LICENSE](LICENSE).
