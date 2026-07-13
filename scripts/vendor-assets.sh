#!/usr/bin/env bash
# vendor-assets.sh — refresh the vendored front-end assets from the npm
# packages pinned in internal/content/assets/package.json (+ lockfile).
#
# Standard npm flow so Renovate can manage upgrades: it bumps package.json /
# package-lock.json like any npm project, and this script (via `make assets`,
# run by CI or the assets-sync workflow) copies the dist files go:embed needs.
# `npm ci` verifies the lockfile's integrity hashes — no hand-copied files.
set -euo pipefail

cd "$(dirname "$0")/../internal/content/assets"

npm ci --no-audit --no-fund

cp node_modules/@xterm/xterm/lib/xterm.js       xterm.js
cp node_modules/@xterm/xterm/css/xterm.css      xterm.css
cp node_modules/@xterm/addon-fit/lib/addon-fit.js xterm-addon-fit.js

echo "vendored from lockfile:"
node -e 'const l=require("./package-lock.json").packages;
for (const p of ["node_modules/@xterm/xterm","node_modules/@xterm/addon-fit"])
  console.log(" ", p.replace("node_modules/",""), l[p].version)'
shasum -a 256 xterm.js xterm.css xterm-addon-fit.js
