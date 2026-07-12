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

## Deliberately deferred

- **Lesson rendering** — stays in the content repo's build; this serves the
  output. Keeps this binary language-homogeneous (no Ruby).
- **OIDC / identity** — the scoring API takes a `userIDFunc(*http.Request)`;
  wiring it to a real session cookie / OIDC provider is a small, isolated
  addition, not baked into the core.
- **Perceptual-hash exercise grading** — the store accepts a `phashGrader`
  func (dHash/Hamming comparison of screenshot proofs); the comparator
  itself is pluggable and not yet ported.
- **Persistent scoring store** — challenges are in-memory, seeded at boot
  from the challenges file (idempotent, stateless-content model). A durable
  solve log (for leaderboards across restarts) would back `Store` with a DB.
