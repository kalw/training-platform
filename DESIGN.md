# Design — training-platform (all-in-one, Kubernetes-only)

## Why one binary

The legacy platform is six repos with three languages, a fork-and-patch
CTFd, a Jekyll plugin pipeline, and Swarm-era assumptions. Operationally
that's six images, six release cadences, and cross-repo contracts (the hash
recipe duplicated in Ruby + bash + Python) that drift. This repo collapses
the *server-side* runtime into one Go program: one image, one release, the
hash contract in exactly one place. Content authoring (writing lessons)
stays a separate build-time concern — it produces static HTML + a challenges
file, which this binary serves and ingests.

## Why the shim stays even though deployment is k8s-only

"Kubernetes-only" is a **deployment** constraint. Course content is still
about Docker: learners run `docker build`, `docker run`, etc. inside their
session. Two things make that work on a k8s deployment:

1. **Session instances are privileged Pods running a DinD image** — the
   learner's `docker` talks to a real dockerd nested in their own Pod.
2. **The Docker-Engine-API shim** (`internal/dockershim`) lets Docker
   *tooling* (an unmodified PWD console, or a learner pointing `DOCKER_HOST`
   at it) drive Kubernetes as if it were a Docker daemon. This was validated
   end-to-end — create/start/exec/attach/logs/rm via the real `docker` CLI,
   and a full session+terminal round-trip through an unmodified PWD console —
   in `../training-deployment/K8S-SANDBOX-DESIGN.md`.

So the binary offers **both** a native k8s session engine (for a k8s-aware
console) and the Docker-API shim (for Docker-native tooling), backed by the
same primitives.

## The proven primitives this is built on

Everything here rests on experiments already run against a `kind` cluster
(documented in the design doc):

- **exec/attach protocol translation.** Docker's hijacked raw stream and
  Kubernetes' SPDY `pods/exec`/`pods/attach` are different wire protocols;
  the translation (101-UPGRADE handshake, per-write flush, 8-byte stdcopy
  framing for non-TTY) works. `internal/dockershim` carries it verbatim.
- **In-cluster routing needs no per-session network attach.** A Pod running
  inside the cluster reaches every other Pod IP directly via the CNI's flat
  network — proven by running the real l2 binary as a bare Pod and hitting a
  second Pod by IP. `internal/router` is the clean re-implementation of that
  host-decode-and-dial logic (kept byte-compatible with the legacy
  `router/host.go` encoding).
- **The hash scoring contract.** `sha256(question+filename)` for challenge
  identity, `sha256(answer+salt)` for flags — reproduced exactly (plain
  concatenation, no separator) so it stays compatible with the lessons build
  that emits the page DOM and the challenges file.

## Component boundaries

```
                         cmd/training  (serve | shim | router | version)
                                │
              ┌─────────────────┼───────────────────────────┐
        internal/server (composes the HTTP surfaces for `serve`)
              │           │            │            │         │
     internal/lessons  scoring    terminal      session   dockershim
      (static site)  (hash API) (ws⇄exec)   (ns+pod)   (DockerAPI⇄k8s)
                                     └────────────┴─────────┘
                                        client-go (Pods, exec, attach)
     internal/router (exposed-port proxy; own listener or in `router`)
```

- `session` and `dockershim` are two faces of the same capability (make
  sandboxes on k8s); a deployment can use either or both.
- `router` is standalone by design — it typically runs as its own in-cluster
  Deployment fronting `*.direct.<domain>`, but is also exposable from the
  composed server.
- `scoring` holds no k8s dependency and is fully unit- + HTTP-tested.

## Security posture

- The **workload** Pod is unprivileged: `runAsNonRoot`, read-only rootfs,
  all caps dropped. The sensitive capability (creating Pods, exec) is an
  RBAC grant on its ServiceAccount, not a privilege on the container.
- RBAC is least-privilege and split: cluster-scoped only for Namespace
  lifecycle (sessions are namespaces, GC'd by TTL); everything else
  (`pods`, `pods/exec`, `pods/attach`, `pods/log`) is a namespaced Role
  confined to the session namespace.
- **Privileged session Pods** (nested dockerd) are allowed only in the
  session namespace via Pod Security Admission `enforce: privileged`, never
  cluster-wide. On clusters that forbid privileged Pods entirely, run
  sessions under a sandboxed RuntimeClass (gVisor/Kata/sysbox) instead.

## The lesson page front-end (terminals)

The rendered page embeds **xterm.js** (the same emulator the legacy PWD SDK
used, three major versions later) rather than a hand-rolled `<div>`: session
shells emit ANSI sequences, and only a real terminal emulator renders them.
The vendored copy is pinned as npm dependencies in
`internal/content/assets/package.json` (+ lockfile) and refreshed by
`make assets` (`npm ci` + copy) — a Renovate-manageable lifecycle instead of
a one-off fetch, with CI enforcing the committed copies match the pins. The terminal WebSocket
carries binary frames for TTY bytes and JSON text frames for control
(resize → `remotecommand.TerminalSizeQueue`), so plain byte-stream clients
(the e2e spec, `websocat`) keep working.

The page reimplements the legacy SDK's *contract* — `terms:` node panels,
`.termN` click-to-run, `{:data-port=}` link rewriting, reconnect, close —
against this binary's session API (create/status/keepalive/delete + TTL GC
server-side). The SDK itself (xterm 2.9.2, webpack 2, socket.io-style
events) is not vendored; see README "Relationship to training-console-pwd".

## Deliberately deferred
- **Identity** — handled by `internal/auth` via **social login** (GitHub /
  Google OAuth2) rather than a generic OIDC provider: fewer moving parts, no
  IdP to run. A provider turns on when its client id/secret env vars are
  present; with none set the platform runs anonymously. Login issues an
  HMAC-signed session cookie, and `Manager.UserID(r)` feeds the scoring
  API's `userIDFunc` hook so solves attribute to a real account.
- **Perceptual-hash exercise grading** — implemented in
  `internal/scoring/phash.go`: a 64-bit dHash (grayscale, 9×8, adjacent-column
  compare) with Hamming-distance matching against a `phash$<hex>[:threshold]`
  flag. Exercise captures arrive as data-URL JPEG/PNG; the reference flag is
  computed at **build time** (`training build`) by rendering the expected
  result page with headless Chrome at 1024×768 and dHashing it (the
  `exercise_result:` front-matter key selects the page; `.png`/`.jpg`
  references skip the browser). This is the Go equivalent of the legacy
  chromium step in `exportChallenges.sh`.

  **Its limit, and the answer to it.** A dHash proves layout, not content —
  rewriting every string on the example result page moves ~1% of the bits even
  at a 32×32 grid, under the noise floor needed for cross-renderer tolerance.
  So exercises that must assert what the page *says* declare
  `exercise_expect:` and are graded by **server-side fetch** instead
  (`/api/v1/challenges/verify`): the platform reaches the learner's Pod
  directly and asserts the body. That is both exact and unforgeable by the
  browser — the inverse of the screenshot proof, which is fuzzy *and* client
  supplied. The target (port/path) is fixed on the challenge at build time and
  the pod is validated against the session engine, so the endpoint is not an
  arbitrary-URL fetcher. phash remains the fallback for genuinely visual or
  client-rendered results.

  The **client** that produces the capture is `exercise-verify.js` (served at
  `/js/`, embedded, not vendored): the learner's result page — served from
  their session Pod through the exposed-port router, a different origin than
  the platform — loads it, html2canvas-screenshots the page, and POSTs the
  proof. The scoring API therefore carries CORS (reflect-origin +
  credentials) so the cross-origin submission attributes the solve. This is
  the port of the exercises-template "result page → verify script → CTFd"
  contract onto the one binary.
- **Persistent scoring store** — challenges are in-memory, seeded at boot
  from the challenges file (idempotent, stateless-content model). A durable
  solve log (for leaderboards across restarts) would back `Store` with a DB.
