# training-platform

The training platform as **one Go binary**, deployed **only on Kubernetes**.

It consolidates the server-side functionality that used to be spread across
six repos (a Go console, a JS SDK, a patched Python CTFd, Jekyll plugins,
Ansible, Helm) into a single statically-linked program with subcommands.
Course content still runs **Docker** — learners open DinD sessions and type
`docker` commands — the "Kubernetes-only" rule is about how the platform is
*deployed*, not what a session can do.

The design and the experiments this is built on are in
[`../training-deployment/K8S-SANDBOX-DESIGN.md`](../training-deployment/K8S-SANDBOX-DESIGN.md):
the Docker-Engine-API→Kubernetes shim (proven against `kind` and a real
unmodified Play-With-Docker console), the in-cluster router that reaches
session Pods with no per-session network attachment, and the hash-based
scoring contract.

## What's in the binary

| Surface | Package | What it does |
|---|---|---|
| **Docker shim** | `internal/dockershim` | Serves a subset of the Docker Engine API backed by Kubernetes (containers→Pods, exec/attach→pods/exec/attach). Keeps "play with docker" content working. Ported verbatim from the proven PoC. |
| **Session engine** | `internal/session` | Kubernetes-native sandboxes: a session is a labelled Namespace, an instance is a privileged Pod. TTL-based GC. |
| **Terminals** | `internal/terminal` | Browser WebSocket ⇄ `pods/exec` (SPDY) — the in-browser shell. |
| **Scoring** | `internal/scoring` | The `sha256(question+filename)` / `sha256(answer+salt)` contract, an in-memory challenge store, and the `/api/v1/challenges/{hash,attempt}` endpoints. Replaces the patched CTFd. |
| **Router** | `internal/router` | Exposed-port routing: decodes `ip<A-B-C-D>-<session>...` hosts and proxies to the Pod IP. Runs in-cluster, where Pod IPs are directly routable. |
| **Lessons** | `internal/lessons` | Serves the pre-rendered static lesson site. |
| **Auth** | `internal/auth` | Social login (GitHub / Google OAuth2) with a signed session cookie; attributes solves to a real user. Anonymous when unconfigured. |
| **Content** | `internal/content` | `training build`: renders Markdown lessons (front matter + `{% quiz %}` / `{% exercise %}` blocks) into PWD-compatible HTML **and** imports the challenges. Exercise flags are the perceptual hash (dHash) of the expected result page, rendered headlessly at build time. |

## Authoring lessons (`training build`)

Lessons are Markdown with YAML front matter and two block types:

```markdown
---
title: Containers — quiz
image: busybox:1.36          # boots the session instance for this lesson
---
# Listing containers

{% quiz %}
Which command lists the running containers?
- [x] docker ps
- [ ] docker ls
{% endquiz %}
```

Exercises declare a reference **result page**; its perceptual hash is computed
at build time (never hand-written):

```markdown
---
image: ghcr.io/kalw/my-broken-nginx:latest   # custom image FROM training-exercises-template
exercise_result: 03-fix-nginx-result.html    # rendered headlessly, dHashed -> phash flag
exercise_threshold: 12
---
{% exercise %}
Fix the web server so the status page renders correctly.
{% endexercise %}
```

`training build --src examples/lessons --out site --salt "$CTFD_SALT"` renders
each lesson to `site/<slug>.html`, writes `site/index.html`, and emits
`site/challenges.json` — the one artifact `serve` seeds scoring from. The
Markdown→HTML render and the challenge import are the **same pass**, so the
page DOM and the challenge store can never disagree on a hash. Exercise
grading needs headless Chrome/Chromium at build time (auto-detected, or
`CHROME_BIN`); an `exercise_result:` that's a `.png`/`.jpg` is hashed
directly with no browser.

See [`examples/lessons`](examples/lessons) for a plain, a quiz, and an
exercise lesson. The quiz/exercise pages only ever confirm that an answer was
*submitted* — never whether it was correct — so the UI can't be used to
brute-force the answer (each choice's hash is in the DOM); outcomes live on
the `/scoreboard`. The [Playwright suite](e2e) proves this, and demonstrates
that the scoring channel *verifies* outcomes but doesn't *prevent* forgery
(a client can POST a correct hash / screenshot directly, bypassing the UI).

### How an exercise solve is submitted (the proof client)

The exercise's **"Test Exercise"** button opens the *learner's own* result
page — served by their session Pod on the exercise image's port, reached
through the exposed-port router — with `?hash_code=…&lessonsDomain=…`
appended. That page carries a tiny loader (baked into
`training-exercises-template`, and into the example
[`03-fix-nginx-result.html`](examples/lessons/03-fix-nginx-result.html)) that
pulls **`/js/exercise-verify.js`** from the platform. `exercise-verify.js`
loads the vendored **html2canvas**, screenshots the page at 1024×768, and
POSTs the capture (a JPEG data-URL) to `/api/v1/challenges/attempt`, which
perceptual-hashes it against the build-time reference and records the solve.
Because the result page is a different origin than the platform, the scoring
API answers the CORS preflight and reflects the origin (with credentials, so
an authenticated solve still attributes). Both `exercise-verify.js` and
`html2canvas.min.js` are embedded in the binary (served at `/js/` and
`/assets/`) and copied into every `training build` output.

### Legacy formatting options (writing-tutorials.md parity)

The renderer supports the authoring contract of the legacy lessons repo
(`training-lessons-ps/writing-tutorials.md`):

| Feature | Status |
|---|---|
| `terms:` front matter (0–6 terminal windows, one instance Pod each, PWD "node" semantics) | ✅ default 1; `0` = no console |
| ` ```.termN ` code blocks (click-to-run in terminal N) | ✅ |
| `[text](/){:data-term=".termN"}{:data-port="XXXX"}` exposed-port links, plus optional `{:data-host-prefix="p"}` and `{:data-protocol="https:"}` | ✅ rewritten live to `[p-]ip<A-B-C-D>-<id>-<port>.<ROUTER_HOST>` once a session is up (same byte layout as the legacy SDK; protocol defaults to the page's, not the SDK's hardcoded `http:`) |
| `SESSION_ID` / `PWD_HOST_FQDN` env inside instances (lesson snippets that echo service URLs) | ✅ injected at Pod creation; note the host is now `ip…-….${PWD_HOST_FQDN}` — `ROUTER_HOST` already includes any `direct.` subdomain |
| `{% quiz %}` / `{% exercise %}` blocks, `exercise_result:` / `exercise_threshold:` | ✅ (same hash contract) |
| Authored exercise demo links — `{:id="exerciseDemo"}` (or `{:class="exerciseDemo"}` with several): Nth marked link ↔ Nth exercise block | ✅ every exercise always renders a **"Test Exercise"** submit button; the marked link *supplies its routing* (adopts `data-port`, href→result-page path, `data-term`, `data-host-prefix`, `data-protocol`) — so the button opens the right result page (often a non-80 port) — while the marked link stays inline as a plain preview. No mark → the button uses the defaults (port 80, `/result.html`) |
| `openConsoleTool('nodeN','editor'/'session')` buttons (PWD file editor / session popup) | ❌ legacy console UI, not ported |
| ssh into an instance (`ssh -p 2223 ip…@direct.…`) | ❌ the k8s engine exposes no sshd |

### Terminals & session lifecycle (what the rendered page does)

Lesson pages embed [xterm.js](https://xtermjs.org) terminals (vendored, see
below). **Start session** boots one instance Pod per terminal and waits for
Running — pods that can never start (e.g. `ImagePullBackOff`) fail fast with
the reason shown in the panel. The page then:

- bridges each terminal to `/terminals/{pod}` (binary frames = TTY bytes,
  text frames = JSON control, currently `{"type":"resize","cols","rows"}` —
  the server drives the exec TTY size through a `TerminalSizeQueue`);
- stores Pod names in `sessionStorage`, so a reload **reattaches** to the
  running Pods instead of leaking new ones (state inside the Pod survives;
  the shell is new);
- pings `POST /api/v1/sessions/{pod}/keepalive` every minute **while the tab
  is visible**, sliding the Pod's *idle window* (`--session-idle-ttl`,
  default 10m) forward. Close the tab — or leave it hidden — and the pings
  stop, so the server GC reaps the Pods after the idle TTL. The *hard* cap
  (`--session-ttl`, default 4h) is never extended: it bounds total session
  length. Coming back to a hidden tab pings immediately and either resumes
  the session or resets the UI if the Pods were reaped;
- **Stop** deletes the Pods and clears the stored session;
- reconnects the WebSocket (with backoff) while the Pod is still alive.

Sessions API: `POST /api/v1/sessions` (create, returns
`pod`/`ip`/`expires_at`), `GET /api/v1/sessions/{pod}` (phase/ready/reason),
`DELETE /api/v1/sessions/{pod}`, `POST /api/v1/sessions/{pod}/keepalive`.
`GET /api/v1/config` serves runtime page config (`router_host` from
`ROUTER_HOST` / `--router-host`), keeping pages build-once/deploy-anywhere.

When `--router-host` is set, `serve` **also answers for exposed-port hosts
itself**: a request whose Host is `ip<A-B-C-D>-<id>[-port].<router host>` is
proxied straight to that Pod IP (the composed server runs in-cluster, where
Pod IPs are routable). Point a wildcard ingress (`*.<router host>`) at the
same Service and `{:data-port=}` links work with no extra deployment; the
standalone `training router` remains for scaled setups.

### Vendored front-end assets

xterm.js and its fit addon are pinned as ordinary npm dependencies in
[`internal/content/assets/package.json`](internal/content/assets/package.json)
(+ lockfile) — the standard manager surface, so **Renovate upgrades them like
any npm project** (grouped by [`renovate.json`](renovate.json)). `make assets`
runs `npm ci` (lockfile-integrity-verified) and copies the dist files next to
the manifest for `go:embed`; the committed copies are enforced against the
pins by a CI check, and the [`assets-sync`](.github/workflows/assets-sync.yml)
workflow regenerates them automatically on Renovate's PRs. The files are
embedded in the binary (served at `/assets/`, no CDN at runtime) **and**
copied into every `training build` output so a built site is self-contained.
To upgrade manually: bump the pin, `make assets`, commit both.

Everything is wired together by `internal/server` and driven by the
`cmd/training` CLI.

## Usage

```
training serve    [flags]   run the composed platform on one port (default :8080)
training shim     [flags]   run only the Docker-API → Kubernetes shim (default :2375)
training router   [flags]   run only the exposed-port router (default :8090)
training version            print build info
```

`serve` mounts everything: lessons at `/`, scoring at `/api/v1/challenges/`,
terminals at `/terminals/{pod}`, and (with `--enable-shim`, on by default)
the Docker API under `/docker/`. Flags mirror env vars (`LESSONS_DIR`,
`INSTANCE_IMAGE`, `ENABLE_SHIM`, `CTFD_SALT`, …).

## Build

Pure Go, no cgo — cross-compiles to every target with no C toolchain.

```sh
make build           # ./bin/training for the host
make test            # go vet + go test -race
make release-build   # dist/ binaries for linux/darwin/windows × amd64/arm64
make image           # multi-arch container (needs buildx)
```

CI ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs vet + race
tests + a gofmt check, cross-compiles the full OS/arch matrix on every push
and PR, runs the Playwright e2e suite in Docker, builds a multi-arch
(`linux/amd64,linux/arm64`) image to GHCR on `main`/tags, and cuts a
goreleaser release (binaries for all targets + checksums) on `v*` tags.

## Testing

```sh
make test                              # Go: vet + race unit/integration tests
docker build -f e2e/Dockerfile -t training-e2e . && docker run --rm training-e2e
```

The end-to-end tests are [Playwright](e2e) and run **fully self-contained in
Docker** — no local Node or Chrome needed, no Kubernetes cluster required.
They cover the lesson UI (Markdown rendering), the quiz submitted-only
behaviour, forged API calls (correct quiz hash / exercise screenshot posted
directly), and the scoreboard. The terminal spec is cluster-gated: run it
against a real cluster with `E2E_CLUSTER=1` (see below).

## Run locally on kind or k3s

The platform deploys only on Kubernetes; any conformant cluster works. Two
zero-cost local options:

### kind — the whole loop is four make targets

```sh
kind create cluster --name training

make dev-deploy     # image build + kind load + lessons render/ConfigMap + helm install/upgrade
make dev-forward    # http://localhost:8080

# then, while iterating:
make dev-image      # code changed   -> rebuild image, load, restart
make dev-lessons    # lessons changed -> re-render, update ConfigMap, restart
make dev-down       # uninstall the release
```

Knobs (all overridable): `KIND_CLUSTER=training`, `DEV_NS=training`,
`DEV_RELEASE=training`, `LESSONS_SRC=examples/lessons`, `DEV_SALT=demo-salt`,
`DEV_ROUTER_HOST=` (overrides the dev default). The dev install
uses [`deploy/helm/dev-values.yaml`](deploy/helm/dev-values.yaml): local
`training-platform:dev` image, lessons from the `lessons` ConfigMap mounted
at `/lessons`, a 5m idle TTL, plain-HTTP cookies, and
`routerHost: direct.127.0.0.1.sslip.io:8080` — so `{:data-port=}` links work
straight through `make dev-forward`: the sslip.io wildcard resolves to
127.0.0.1, the port-forward carries the request in, and `serve` proxies to
the Pod IP encoded in the hostname (needs internet DNS for sslip.io). The restart on
`dev-lessons` is what re-seeds the challenge store from `challenges.json`
(the ConfigMap alone propagates files, not challenges). The `assets/` subdir
of a built site is not in the ConfigMap (ConfigMaps are flat) — the binary
serves `/assets/` from its embedded copy.

### k3s

```sh
curl -sfL https://get.k3s.io | sh -      # single-node k3s
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml   # (sudo chmod +r it, or copy to ~/.kube/config)

# k3s uses containerd, not Docker — import the image straight into it
docker build -t training-platform:dev .
docker save training-platform:dev | sudo k3s ctr images import -

helm install training deploy/helm/training-platform \
  --namespace training --create-namespace \
  --set image.repository=training-platform --set image.tag=dev \
  --set image.pullPolicy=IfNotPresent \
  --set serve.salt=demo-salt
# k3s ships Traefik; set ingress.enabled=true + ingress.host to expose it,
# or port-forward as above.
```

Both give the platform a Service account with the least-privilege RBAC the
chart defines, and a `training-sessions` namespace where privileged DinD
session Pods are allowed (Pod Security scoped to that namespace only). Point
the cluster-gated Playwright terminal test at either with
`E2E_CLUSTER=1 E2E_PORT=8080 npx playwright test tests/terminal.spec.ts`
(from `e2e/`, against a running `port-forward`).

## Deploy (Kubernetes only)

A Helm chart is in [`deploy/helm/training-platform`](deploy/helm/training-platform):

```sh
helm install training deploy/helm/training-platform \
  --set serve.salt=$CTFD_SALT \
  --set ingress.enabled=true --set ingress.host=training.example.com
```

It renders a Deployment (unprivileged, read-only rootfs, drops all caps), a
Service, optional Ingress (including the `*.direct.<domain>` wildcard for the
port router), and **least-privilege RBAC**: a cluster-scoped Role for
Namespace lifecycle + GC, and a namespaced Role for `pods` / `pods/exec` /
`pods/attach` / `pods/log` confined to the session namespace. Privileged
session Pods (nested dockerd) are permitted **only** in that one namespace
via Pod Security Admission — never cluster-wide.

## Scope / status

Real and tested here: the Docker shim (ported from the validated PoC), the
scoring contract (unit + HTTP integration tests), the router host-decoder
(kept byte-compatible with the legacy console, unit-tested), the k8s session
engine (pod lifecycle unit-tested against a fake clientset), the terminal
bridge with TTY resize, the lessons build (renderer unit tests + Playwright
UI suite), and the browser session lifecycle (verified live against kind:
boot → resize → click-to-run → reload-reattach → stop).

### Relationship to `training-console-pwd` / the JS SDK

What was worth reusing from the legacy console has been ported, not
rewritten from scratch: the Docker shim comes verbatim from the proven PoC,
the router keeps the console's host encoding byte-compatible, and the lesson
page implements the SDK's behavioural contract (terms, click-to-run,
data-port links, resize, close). The SDK's *code* is not vendored — it is
frozen on xterm 2.9.2 / webpack 2 / Node 8 and drags a session protocol
(socket.io-style events, Swarm-era instance API) this platform replaces with
plain WebSocket + `pods/exec`. Server-side, the PWD session manager is
Docker/Swarm-centric by design; the k8s-native engine here is its deliberate
replacement, not a fork. Features that still only exist in the legacy
console (file-editor popup, ssh gateway, uploads) stay there — port them
behind the same lifecycle endpoints if course content needs them.
