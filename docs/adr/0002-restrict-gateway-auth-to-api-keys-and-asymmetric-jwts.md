# ADR-0002: Resolve WebSocket auth via API keys and asymmetric JWTs only

**Status**: Accepted
**Date**: 2026-03-29
**Ticket**: fix/cli-auth-flow

## Context

The CLI handles three distinct credential types: an opaque **admin token** for the provisioning REST API, a per-tenant **API key** validated by the gateway's KeyRegistry, and **JWTs** for gateway WebSocket auth. The original `subscribe`/`publish` flow was broken: when no explicit credential was given it fell back to sending the provisioning admin token as a Bearer token, which the gateway rejects because it is not a JWT. Separately, `token generate` defaulted to HS256, which the gateway refuses in production (only ES256/RS256/EdDSA are accepted), so operators got silently rejected tokens. The gateway also began requiring a `jti` claim on every JWT (feat/token-revocation) to support session revocation, and a parallel change (feat/cli-public-read-mode) established that an API key alone grants read-only subscribe while publish always requires a JWT.

## Decision

Fix and formalize CLI auth resolution around the three credential roles:
- `resolveWSAuth` checks the **API key first** (flag, then context) and **never falls back to the admin token** for gateway connections — the admin token is exclusively for the provisioning REST API.
- When no credential resolves, the CLI sends nothing and lets the **gateway decide** (accept if auth is disabled, reject if enabled). The CLI never pre-blocks a connection.
- **HS256/HMAC support is removed entirely**: the `--secret` flag, the `HMACSecretEnc` context field, and its `HMACSecret()` accessor are deleted. `token generate` errors clearly if no algorithm/key is available.
- `key create --generate` mints an **ES256 keypair locally**, registers the public key with provisioning, and saves the private key to `~/.config/sukko/keys/<tenant>/<key-id>.pem`. `token generate` **discovers the key by filesystem scan**, not via a context struct field.
- Every generated token includes a UUID **`jti`** (printed to stderr so stdout stays a pipe-safe raw JWT). An **API key alone permits read-only subscribe**; publish requires a JWT.

## Consequences

- The full production auth path (keygen → register → mint → connect) is testable with the CLI alone, no external JWT tooling.
- Credential roles are unambiguous: admin token = provisioning REST; API key = gateway subscribe (read); JWT = gateway publish/full access.
- Dropping HMAC is a one-way door: reintroducing HS256 for "convenience" would reopen the silent-rejection failure mode and weaken the security posture.
- Old context files carrying `HMACSecretEnc` still load (unknown JSON fields are ignored) — no migration needed.
- Key discovery by directory scan avoids a context schema change but couples token generation to an on-disk path convention.

## Alternatives rejected

- **Fall back to the admin token for WebSocket auth** — the admin token is not a JWT; this was the original bug.
- **Keep HS256 as the `token generate` default** — the gateway rejects it in production; symmetric secrets are a weaker model.
- **Store the generated private key in the context struct** — filesystem discovery needs no schema change and keeps keys as ordinary `.pem` files.
- **Have the CLI pre-check credentials and block unauthenticated connections** — the gateway is the authority; local pre-checks duplicate and can diverge from it.
