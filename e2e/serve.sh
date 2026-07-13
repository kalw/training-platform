#!/usr/bin/env bash
# Render the example lessons into a site (this also imports the challenges,
# computing the exercise phash via headless Chrome) and serve it. Playwright's
# webServer waits for the port. No Kubernetes cluster is required — serve
# degrades the terminal/session endpoints to 503 without one, which is fine
# for every test except the cluster-gated terminal spec.
#
# Environment-agnostic:
#   TRAINING_BIN  prebuilt binary to use (set in the e2e Docker image); if
#                 unset, the binary is built with `go build`.
#   CHROME_BIN    headless Chrome for the exercise phash. If unset, we derive
#                 Playwright's bundled chromium (present in the Docker image);
#                 on a dev machine `training build` auto-detects system Chrome.
set -euo pipefail
E2E_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$E2E_DIR/.."

PORT="${E2E_PORT:-8099}"
WORK="$(mktemp -d)"
SITE="$WORK/site"

BIN="${TRAINING_BIN:-}"
if [ -z "$BIN" ]; then
  BIN="$WORK/training"
  go build -o "$BIN" ./cmd/training
fi

# Derive Playwright's bundled chromium for the exercise phash render. Resolve
# @playwright/test from the e2e dir (that's where node_modules lives), not the
# repo root we cd'd into.
if [ -z "${CHROME_BIN:-}" ]; then
  CHROME_BIN="$(cd "$E2E_DIR" && node -e 'try{process.stdout.write(require("@playwright/test").chromium.executablePath())}catch(e){}' 2>/dev/null || true)"
  [ -n "$CHROME_BIN" ] && export CHROME_BIN
fi

"$BIN" build --src examples/lessons --out "$SITE" --salt e2e-salt

exec "$BIN" serve \
  --addr ":$PORT" \
  --lessons-dir "$SITE" \
  --challenges-file "$SITE/challenges.json" \
  --router-host "e2e.direct.test" \
  --enable-shim=false
