# Design proposal — Kubernetes-native sandbox & deployment

Status: **proposed**. Companion to
[`training-console-pwd/docs/k8s-provisioner-design.md`](https://github.com/kalw/training-console-pwd/blob/main/docs/k8s-provisioner-design.md),
which scopes a *native* k8s provisioner inside PWD (multi-week, touches
`docker.DockerApi` callers). This proposal takes a different, smaller path
to the same rebrand goal.

## Objective

Move the platform's execution substrate from Docker Swarm + privileged
DinD hosts to Kubernetes, without a from-scratch rewrite of PWD's session
engine.

## Chosen approach: Docker-API shim, not a new provisioner

PWD's console never talks to Docker directly except through the standard
Docker Go SDK client, configured entirely by `DOCKER_HOST`
(`config/config.go`, consumed by `console.pwd.dockerHost` in the Helm
chart / `PWD_DOCKER_HOST` in compose — this plug point already exists and
is exercised today for remote-engine offload).

Instead of teaching PWD a Kubernetes backend, put a **shim service in
front of the Kubernetes API that speaks the Docker Engine REST API**. Point
`DOCKER_HOST` at it. PWD is unmodified — it still believes it's driving a
Docker daemon; every call is translated to Kubernetes Pod/Namespace/Exec
operations. This is the "fake the docker client" framing: not a change to
docker interpretaion inside PWD, but an impersonation layer underneath it.

Prior art to build from rather than starting blank: **kubedock**
(`github.com/joyrex2001/kubedock`) already implements this pattern
(containers, exec, logs, basic networking → Kubernetes). Evaluate forking
it (same fork-and-patch strategy already used for PWD/CTFd) over a
green-field shim; it will need extensions (see Gaps below) regardless.

### Why this over the native-provisioner note

| | Docker-API shim | Native k8s provisioner in PWD |
|---|---|---|
| PWD code changes | none (`DOCKER_HOST` only) | new `provisioner.NewK8s`, re-implements attach streaming, CheckPorts/CollectStats tasks, l2 IP feed |
| Blast radius | one new component, isolated | touches PWD's core scheduler/provisioner interfaces |
| Effort | shim already has an OSS base to extend | multi-week from the existing note's own estimate |
| Ceiling | bounded by how much of the Docker API surface must be faithfully emulated | can be a first-class k8s citizen (quotas, scheduler awareness) |

Recommendation: ship the shim first as the path to "rebrand to k8s" now;
keep the native-provisioner note as a longer-term option if the shim's
translation overhead (see Gaps) becomes the bottleneck.

## Session model (shim → Kubernetes)

| PWD/Docker concept | Kubernetes object |
|---|---|
| session | Namespace `pwd-<sessionId>`, labeled for GC/TTL |
| container create/start | Pod create (single-container, DinD image) |
| container stop/remove | Pod delete |
| `exec create` + `exec start` (hijacked stream) | `pods/exec` subresource (SPDY/WebSocket) — protocol translation, not passthrough |
| `attach` / logs | `pods/log` (`?follow=true`) + exec for interactive stdin |
| container IP (used by l2 router) | Pod IP, read back via the shim's `inspect` response so PWD's existing IP-learning path needs no change |
| networks (per-session overlay) | skip: pods in the same namespace already share flat networking; emulate `network create/connect` as no-ops returning success |
| events stream | Kubernetes `watch` on Pods in the session namespace, translated to Docker event JSON |
| stats | `metrics.k8s.io` (requires metrics-server) or cAdvisor passthrough, translated to Docker stats JSON |
| images / build | no-op success (exercise images are pre-built in `ghcr.io/kalw/*` and referenced by tag; nothing pulls or builds through the shim) |

## Real deployment topology

- Cluster: any conformant Kubernetes (kubeadm, EKS/GKE/AKS). Drops the
  hard swarm-mode + `xt_ipvs` kernel module requirement entirely — that
  requirement is Ansible/compose-specific
  ([`pre_docker.yml`](roles/learningtools_migration/tasks/pre_docker.yml)),
  not present in the Helm path.
- Shim: `Deployment` + `Service` (ClusterIP, e.g. `docker-shim.pwd.svc`) in
  a dedicated namespace, with a `ServiceAccount` bound to a `Role`/
  `ClusterRole` scoped to create/delete Namespaces+Pods+exec only under
  the `pwd-*` prefix (least privilege — the shim, not PWD, ends up holding
  the cluster-sensitive credential).
- Console: `charts/training-platform` already has the plug point —
  `console.pwd.dockerHost` → `tcp://docker-shim.pwd.svc:2375`. This drops
  the chart's current privileged/hostPath branch
  ([`templates/console.yaml`](charts/training-platform/templates/console.yaml))
  entirely; the console pod becomes unprivileged.
- Session pods still need `privileged: true` (nested dockerd for `docker
  build`/`docker swarm init` lessons) — that privilege moves from the
  console/host to short-lived, namespace-isolated session pods. Mitigate
  with a `RuntimeClass` (gVisor/Kata or sysbox) if available on the target
  cluster; otherwise apply Pod Security Admission `privileged` only to
  `pwd-*` namespaces, never cluster-wide.
- Ingress: **correction from the integration test below** — the browser
  terminal does *not* go through the l2 router. `PWD's own process` opens
  `/containers/{id}/attach` directly over `DOCKER_HOST` (i.e. straight to
  the shim). l2 is only used for direct exposed-port access
  (`http://ip-x-x-x-x.direct.domain:port`), which is genuinely orthogonal
  and unchanged by this proposal — but the terminal itself is not, and
  turned out to already work (see below).

## Gaps / risks (why this is a design, not a merge)

- **Exec/attach protocol — de-risked.** Docker's hijacked-TCP stdcopy
  multiplexing and Kubernetes' SPDY/WebSocket `pods/exec`/`pods/attach` are
  different wire protocols; this was flagged as the crux of the shim. The
  PoC + integration test (below) confirm it translates cleanly for both
  exec and the terminal's attach path, TTY and non-TTY.
- **The L2 NetworkConnect call — solved, see "Swarm removal and the L2 fix"
  below.** `sessionNetworkProvisioner.SessionNew` (renamed from
  `overlaySessionProvisioner`, `provisioner/session_network.go`)
  unconditionally calls Docker `NetworkCreate` then
  `NetworkConnect(L2ContainerName, sessionNetwork, ip)` on every session —
  attaching the L2 router's own container to that session's network so L2
  can route to instance IPs. For a k8s deployment this call still targets a
  container the shim never created and has no knowledge of; the shim
  answers it as a no-op (fabricated network IDs, a fabricated non-routable
  IP for any container name it doesn't manage) so `SessionNew` doesn't
  error and abort. That's sufficient for the terminal (which never goes
  through l2 — see above) but doesn't give l2 real connectivity for
  exposed-port routing. The actual fix isn't in this call at all: run L2
  itself as an in-cluster Pod, and it gets direct L3 routability to every
  Pod IP via the CNI's flat pod network — no NetworkConnect equivalent
  needed. Verified against `kind`; see below.
- **Container naming.** PWD names instance containers
  `<sessionID[:8]>_<xid>` (underscore included) and reuses that exact
  string as the identifier for every subsequent Docker API call — invalid
  as a Kubernetes object name (DNS-1123). The shim now sanitizes
  Docker name → Pod name and caches the mapping; a real component needs
  the same translation, consistently, everywhere a name crosses the
  boundary (create, start, inspect, exec, attach, logs, delete).
- **Swarm-mode lessons.** Multi-engine `docker swarm init` content doesn't
  map to single-pod sessions any more than it would under the native
  provisioner. Scope k8s-backed sessions to single-instance/kubeadm-in-pod
  lessons first (mirrors the existing note's own phasing).
- **Namespace GC.** Session TTL (4h) needs a controller (shim-owned reaper
  or `kube-janitor`) — Kubernetes has no built-in namespace TTL.
- **Multi-tenancy.** Default-deny `NetworkPolicy` between `pwd-*`
  namespaces so learner sessions can't reach each other or the cluster
  control plane. Not exercised by the integration test (single fixed
  namespace, PWD's own network segmentation deliberately unused).

## Proof-of-concept scope

**Result: built, all 5 criteria pass — go.** Prototype at
[`../docker-k8s-shim-poc`](../docker-k8s-shim-poc) (own directory, not yet
`ghcr.io/kalw/*`); see its README for the run-it-yourself steps and the
protocol details below.

Narrowest slice that answers the open question ("can a Docker-API shim
over Kubernetes support what a session needs?") without touching PWD,
Helm, or Ansible yet.

**Harness:** the stock `docker` CLI / Go SDK pointed at the shim
(`docker -H tcp://localhost:2375 ...`) against a local `kind` cluster —
not PWD. This isolates "does the shim work" from "does PWD's session
lifecycle work", which is deliberately deferred to rollout step 2.

**In scope (endpoints):**
- `GET /_ping`, `GET /version`
- `POST /containers/create` (single container, `--privileged`, no networks)
- `POST /containers/{id}/start`
- `GET /containers/{id}/json` (inspect — must return `NetworkSettings.IPAddress` = pod IP)
- `POST /containers/{id}/exec` + `POST /exec/{id}/start` (TTY, interactive — this is the risky one, see below)
- `GET /containers/{id}/logs?follow=true`
- `POST /containers/{id}/stop`, `DELETE /containers/{id}`

**Explicitly out of scope for the PoC:** networks, build/images, stats,
events, swarm, more than one container per "session", namespace-per-session
(use one fixed namespace), RBAC hardening, TTL/GC, ingress/l2 routing, any
PWD/Helm/Ansible wiring.

**Environment:** `kind create cluster`; run the shim out-of-cluster against
the kind kubeconfig (fastest iteration loop — no image build/push needed
each change). Base it on kubedock if its exec/attach already passes the
success criteria below; otherwise a minimal greenfield shim using
`client-go` + `docker/docker/api/types` request/response structs.

**Success criteria (all against kind, via the plain `docker` CLI) — all passed:**
1. ✅ `docker create --privileged busybox:1.36 sleep 3600` + `docker start` → a Pod exists in kind (`kubectl get pods`). (Ran against `busybox`, not the real `ghcr.io/kalw/training-console-pwd:dind` image — nested dockerd-in-kind wasn't needed to validate protocol translation, and is called out as a separate follow-up below.)
2. ✅ `docker exec -it <id> sh` → interactive shell over `pods/exec` (SPDY), verified with a pty-backed harness — this was the real unknown (SPDY vs Docker's hijacked-TCP framing) and it works.
3. ✅ `docker logs -f <id>` → streams live pod logs.
4. ✅ `docker inspect <id>` → `NetworkSettings.IPAddress` is a real, reachable pod IP.
5. ✅ `docker rm -f <id>` → pod deleted.

**Go/no-go:** go — proceed to rollout step 2. (The plan was: if criterion 2
needed disproportionate custom protocol work, fall back to the native
provisioner note. It didn't — see the PoC README for the fixes required,
none of which were disproportionate.)

**What the PoC surfaced that the design doc didn't anticipate** — four
protocol details, all now handled in the prototype (see its README for
specifics): exec-start must return HTTP `101 UPGRADED` (modern Docker
clients reject a plain `200`); writes to the hijacked connection must be
flushed per-write or output is silently dropped when the exec'd process
exits quickly; non-TTY exec/logs need Docker's 8-byte stream-multiplexing
frames (TTY mode is raw passthrough only, and Kubernetes doesn't separate
stdout/stderr so everything is framed as stdout); and `docker rm` hits a
bare `DELETE /containers/{id}` with no action suffix, which needs its own
route. None of these needed anything beyond the Docker Engine API's own
documented wire format — no new unknowns, just under-documented ones.

**Estimated effort:** 3–5 engineer-days. **Deliverable:** a standalone
prototype (own directory/repo, not yet in the `ghcr.io/kalw/*` CI
pipeline) plus a short README with the kind setup and the five test
commands above — no chart or Ansible changes at this stage.

## Integration test: real PWD against the PoC shim

**Result: works end to end — session, instance, and interactive terminal,
through unmodified PWD.** Extended the PoC shim (still in
[`../docker-k8s-shim-poc`](../docker-k8s-shim-poc), same prototype, not yet
a real component) just enough to survive PWD's actual provisioning flow,
then drove the real `training-console-pwd` binary (built from this repo's
checkout, unmodified) against it:

1. Built the `training-console-pwd` image locally, ran it with
   `DOCKER_HOST=tcp://host.docker.internal:2375` (the shim, reachable from
   the container via Docker Desktop's host gateway), `-network-driver=bridge`.
2. `POST /` (PWD's own `NewSession` API) → real session created.
3. `POST /sessions/{id}/instances` with `busybox:1.36` → PWD's
   `ContainerCreate` → shim → **a real Pod, `Running`, in `kind`** — same
   proof point as PoC criterion 1, now through PWD's actual provisioner
   code path (`provisioner/dind.go`), not a hand-crafted `docker` CLI call.
4. Connected to `/sessions/{id}/ws/` with a raw WebSocket client (PWD's
   custom `{name, args}` socket protocol) and sent an `instance terminal
   in` event with `echo hello-from-terminal`. Got back `instance terminal
   out` with the real shell's echoed output — the keystroke went browser
   protocol → PWD → shim → `pods/attach` → the container's actual shell →
   back the same path.

**What this took beyond the PoC** (all now in the shim, see its updated
README): honoring PWD's `?name=` container-naming instead of minting our
own; a Docker-name → Kubernetes-Pod-name sanitizer (PWD's names contain
`_`, invalid in k8s); `/networks/create` and `/networks/{id}/connect` as
no-ops (see the L2 gap above); per-network IP tracking so
`ContainerIPs()`'s network-name-keyed map resolves; and
`/containers/{id}/attach` (distinct from exec — bridges to the pod's own
process 1 via the k8s `attach` subresource, which is the actual terminal
path, not exec as originally assumed).

**What this didn't test (at the time):** exposed-port access via l2 — see
the next section, now solved; CTFd scoring (orthogonal, never touches
Docker); multi-instance sessions; privileged nested dockerd actually
running inside a pod in `kind` (used `busybox`, not the real
`ghcr.io/kalw/training-console-pwd:dind` image, to isolate
protocol-translation risk from container-runtime/CNI risk — still an open
question, see rollout step 3 below).

## Swarm removal and the L2 fix

Two follow-up asks: retire Docker Swarm from the platform entirely (no
longer relevant), and actually close the L2/Kubernetes gap flagged above
rather than leaving it as a documented limitation — "even if it means a
rewrite."

**Swarm removal, `training-lessons-ps`:** 11 lessons whose core subject was
Docker Swarm (swarm mode/stacks/secrets/config, the orchestration
workshop/HOL series, Docker Flow Proxy) removed; 6 more lessons had dead
links to them cleaned up (landing pages, and inline "next step" references
in lessons that survive on their own merits — e.g.
`microservice-orchestration`, `beginner-linux`). Lessons that only
*mentioned* Swarm in passing, or used general clustering concepts
(MongoDB/Redis replica sets, whose Swarm usage is infra scaffolding, not
the subject) were kept.

**Swarm removal, `training-console-pwd`:** removed `SwarmInit`/`SwarmJoin`
(and the `SessionSetup` stack-config fields that triggered them,
`IsSwarmManager`/`IsSwarmWorker`), `GetSwarmPorts` and its scheduler task,
the `-network-driver` flag (was `overlay` by default — the one genuinely
Swarm-specific piece, since "overlay" requires Swarm mode and spans
multiple physical engines; the per-session network driver is now always
hardcoded `"bridge"`), and the `CheckSwarmPortsEvent`/`CheckSwarmStatusEvent`
*producers* (the event names/types themselves stay — they're a stable,
frontend-visible contract that the Kubernetes cluster-status tasks already
emit under for UI compatibility; only the Swarm-mode-specific producers of
those events were removed). All builds and the full test suite pass
(`docker build .` + `go test ./...` inside the build image — no local Go
toolchain, matching this repo's documented workflow).

**A course-correction worth recording:** the first pass also deleted
`NetworkConnect(L2ContainerName, sessionNetwork, ip)` from
`session_network.go` (renamed from `overlay.go`) and the network-membership
bookkeeping in `l2.go` (`connectNetworks`/`monitorNetworks`), reasoning
that L2 should just be placed somewhere with routability instead. That
conflated two different things: `NetworkConnect` to a per-session **bridge**
network is a normal Docker operation that works fine without Swarm and is
what makes L2 reach instance IPs on the *existing, currently-working*
single-host deployment (`console_network_driver` already defaulted to
`"bridge"` in Ansible) — deleting it would have silently broken
exposed-port routing on every non-k8s deployment. That call was restored.
The actual Swarm-specific piece was narrower: only the option to set the
driver to `"overlay"` needed Swarm, and that's gone for good.

**Swarm removal, `training-deployment`:** since `-network-driver` no longer
exists as a PWD flag at all, `console_network_driver` "bridge"-only means
the Podman-can't-do-Swarm sidecar workaround
(`tasks/pre_docker.yml` — a nested `docker:27-dind` container, auto swarm-init,
gated entirely on `console_network_driver == "overlay"`) can never trigger
and was deleted outright, along with `swarm_docker_host`/
`podman_swarm_dind_*` variables, the `NETWORK_DRIVER` env template line and
compose CLI flag (removing this one was mandatory, not cleanup — PWD would
now refuse to boot on an unrecognized flag if it were left in), the
molecule scenario's Swarm-active assertion (which imported the
now-deleted task file), and stale "needs Swarm mode" / `xt_ipvs` kernel
module claims in the Helm chart and top-level READMEs. `ansible-lint
--profile production` shows 42 pre-existing `var-naming[no-role-prefix]`
violations, none in files this touched and none new — confirmed by diffing
which files/lines the violations land in before asserting "no regression."

**The L2/Kubernetes gap — now actually fixed, not just documented.**
`router/l2/l2.go` needed **zero code changes**: its `director()` already
just dials `info.InstanceIP:port` directly, and `connectNetworks()`/
`monitorNetworks()` already degrade gracefully with no Docker socket
present (a missing `SessionsFile` is tolerated at startup; `Events()`
failing leaves an idle no-op goroutine, not a crash). Built the real l2
binary, loaded it into `kind` as a plain Pod — no `docker.sock`, no
`DOCKER_HOST` — and confirmed:
- it boots and serves its own `/ping` health endpoint normally;
- from inside that pod, a direct HTTP request to a second pod's IP (no
  `NetworkConnect`, no attachment of any kind) succeeds — Kubernetes' flat
  pod network (kindnet CNI) already routes pod-to-pod without it.

The fix is placement, not code: deploy L2 as an in-cluster
Deployment/DaemonSet for Kubernetes targets, keep the existing
`NetworkConnect`-based container for classic single-host Docker targets.
Same binary, same `director()`, two deployment shapes — matching the
platform's existing "one artifact per component, configured differently
per environment" pattern. Exposed-port routing (`*.direct.domain`) on
Kubernetes now has a proven path; wiring an in-cluster L2 into the Helm
chart is the remaining implementation work, not a research question.

## Rollout plan

1. ✅ Proof-of-concept (above) — go.
2. ✅ Extend the shim enough for PWD's real provisioning + terminal flow
   (above) — works.
3. ✅ Remove Swarm from the platform (lessons + `training-console-pwd` +
   `training-deployment`) and fix the L2/Kubernetes gap — an in-cluster L2
   Pod has direct, code-free routability to session Pods, proven against
   `kind`.
4. **Next:** validate a real DinD lesson image (nested `dockerd`,
   privileged) actually runs inside a pod in a target cluster — this
   integration test deliberately used `busybox` to isolate protocol risk
   from container-runtime risk, and that substitution needs closing before
   claiming lesson parity. Then wire an in-cluster L2 Deployment into the
   Helm chart (new work, but no longer a design question — see above) and
   point `console.pwd.dockerHost` at the shim for real.
5. Fork/extend a Docker-API shim (kubedock or equivalent, or harden this
   prototype) into a real, `ghcr.io/kalw/*`-published component.
6. Flip the chart's default: drop the privileged hostPath branch, ship the
   shim (and the in-cluster L2) as chart dependencies/subcharts.
7. Update Ansible: `dev_mode` and the classic single-host Docker path
   remain supported (documented, not removed) until the shim is proven in
   production; new deployments default to the k8s+shim path. (Swarm itself
   is gone regardless of which path is used — that removal isn't
   conditional on the k8s migration landing.)
8. Update this doc and the legacy
   [HIGH-LEVEL-DESIGN.md](https://github.com/kalw/training-deployment/blob/main/HIGH-LEVEL-DESIGN.md)'s
   "Console execution engine" section as each phase lands.

> **Moved here from `training-deployment` (2026-07-20).** This document
> records the experiments the platform is built on; the roll-out phases above
> describe the *legacy* stack's migration and are kept as history. Current
> status of that consolidation lives in [MIGRATION.md](MIGRATION.md).
