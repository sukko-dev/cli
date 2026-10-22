#!/usr/bin/env bash
# e2e/run.sh — bootstrap and run the hermetic CLI e2e suite.
#
# The CLI e2e drives the real `sukko` binary against a live docker-compose stack. Post
# ADR-0038 every service needs a signature-valid license; released images verify the
# production key, which we cannot mint. So this script builds the platform images from
# a sukko source checkout with the `sukko_e2e` build tag (which embeds a regenerated
# dev signing key), tags them as the exact refs the CLI's embedded compose references
# (so `sukko up`, which never force-pulls, uses them), and mints a matching dev license.
#
# Requirements: docker, go, and a sukko checkout (set SUKKO_DIR, or place it at ../sukko
# or .sources/sukko). To rotate the dev key, delete $SUKKO_DIR/ws/internal/shared/license/keys/sukko.dev.*.
set -euo pipefail

cd "$(dirname "$0")/.." # repo root

# --- resolve the sukko source checkout ------------------------------------------------
SUKKO_DIR="${SUKKO_DIR:-}"
if [ -z "$SUKKO_DIR" ]; then
  for cand in ../sukko .sources/sukko; do
    if [ -d "$cand/ws" ]; then SUKKO_DIR="$(cd "$cand" && pwd)"; break; fi
  done
fi
if [ -z "$SUKKO_DIR" ] || [ ! -d "$SUKKO_DIR/ws" ]; then
  echo "ERROR: sukko checkout not found — set SUKKO_DIR, or place it at ../sukko or .sources/sukko" >&2
  exit 1
fi
echo "==> sukko source: $SUKKO_DIR"

KEYS_DIR="$SUKKO_DIR/ws/internal/shared/license/keys"

# --- 1. dev license keypair (embedded by sukko_e2e images; signs the minted key) ------
if [ ! -f "$KEYS_DIR/sukko.dev.key" ]; then
  echo "==> generating dev license keypair"
  ( cd "$SUKKO_DIR/ws" && go run ./internal/shared/license/genkeys )
else
  echo "==> reusing existing dev license keypair (delete $KEYS_DIR/sukko.dev.* to rotate)"
fi

# --- 2. build the platform images with the sukko_e2e tag, under the CLI's compose refs -
# Native platform (arm64 on Mac, amd64 on CI) — the images run on this same host. Docker
# layer cache makes re-runs fast when nothing changed.
build_img() { # <dockerfile-dir> <image-ref>
  echo "==> building $2 (sukko_e2e)"
  docker build \
    -f "$SUKKO_DIR/ws/build/$1/Dockerfile" \
    --build-arg GO_BUILD_TAGS=sukko_e2e \
    -t "$2" \
    "$SUKKO_DIR/ws"
}
build_img provisioning ghcr.io/sukko-dev/sukko-provisioning:latest
build_img server       ghcr.io/sukko-dev/sukko-server:latest
build_img gateway      ghcr.io/sukko-dev/sukko-gateway:latest
build_img tester       ghcr.io/sukko-dev/sukko-tester:latest

# --- 3. mint a dev Community license (signed by the regenerated dev key) ---------------
echo "==> minting dev Community license"
SUKKO_LICENSE_KEY="$(
  cd "$SUKKO_DIR/ws" && go run ./internal/shared/license/gentoken \
    --key internal/shared/license/keys/sukko.dev.key \
    --edition community --org 'E2E' --expires +1y
)"
export SUKKO_LICENSE_KEY
if [ -z "$SUKKO_LICENSE_KEY" ]; then
  echo "ERROR: gentoken produced an empty license" >&2
  exit 1
fi

# --- 4. run the suite -----------------------------------------------------------------
echo "==> running CLI e2e suite"
exec go test -tags e2e ./e2e/... -timeout 25m -count=1 "$@"
