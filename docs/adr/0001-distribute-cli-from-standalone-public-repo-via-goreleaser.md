# ADR-0001: Distribute the CLI from a standalone public repository via GoReleaser

**Status**: Accepted
**Date**: 2026-03-24
**Ticket**: feat/cli-binary-releases

## Context

The CLI originally lived inside the private monorepo and shipped only as a GHCR Docker image. Developers had to build from source or run a container — friction for onboarding, unusable in external CI, and impossible for casual evaluation. Native binaries were needed for macOS/Linux/Windows across amd64 and arm64, installable via Homebrew, Scoop, and direct download, with checksums and reproducible version stamping. A hard constraint drove the structure: the monorepo is private, and GoReleaser's private-monorepo-to-public-release path requires the paid GoReleaser Pro tier, whereas GoReleaser OSS works natively on a public repo.

## Decision

Extract the CLI into a standalone public repository (`sukko-cli`) with its own `go.mod`, and release it with GoReleaser OSS driven by a single `.goreleaser.yml`. The CLI's only two monorepo dependencies are severed by inlining: the `version` package is copied verbatim, and the one needed `platform.BaseConfig` struct is inlined locally. Pushing a `v*` git tag triggers a GitHub Actions workflow that cross-compiles all six OS/arch targets, publishes a GitHub Release with archives + SHA256 checksums + `.deb`/`.rpm` assets, and pushes manifests to separate `homebrew-tap` and `scoop-bucket` repositories (via a cross-repo token secret). Pre-release tags (containing `-`) are marked pre-release and do NOT update the stable tap/bucket. Version, commit, and build time are injected via ldflags. This mirrors the industry norm (gh, doctl, stripe-cli).

## Consequences

- Public binaries install in two commands per platform; `go install ...@latest` also works.
- The CLI is fully decoupled from the private monorepo build; contributors can work without monorepo access.
- The inlined `version` package and `BaseConfig` struct must be kept in sync with the monorepo by hand — a small, deliberate drift risk.
- Three GitHub repos plus a cross-repo PAT must exist and be maintained; the token is a shared secret to rotate.
- `.deb`/`.rpm` ship as Release assets only — there is no hosted apt/yum repository, so users run `dpkg -i`/`rpm -i` manually.
- No code signing/notarization, no self-update; those remain out of scope.

## Alternatives rejected

- **Keep the CLI in the private monorepo and release with GoReleaser Pro** — requires a paid tier and still exposes a private→public release path; the public repo is cheaper and simpler.
- **Hosted apt/yum repository** — operational overhead not justified; GitHub Release assets suffice.
- **Continue Docker-only distribution** — leaves the onboarding/CI/evaluation friction unsolved.
