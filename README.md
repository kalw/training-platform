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
and PR, builds a multi-arch (`linux/amd64,linux/arm64`) image to GHCR on
`main`/tags, and cuts a goreleaser release (binaries for all targets +
checksums) on `v*` tags.

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
engine and terminal bridge (compile against client-go; the underlying
exec/attach path is the one proven end-to-end in the design doc).

Deliberately out of scope for this repo: lesson *authoring/rendering* (stays
a build-time concern of the content repo — this serves the static output and
ingests the challenges file it produces) and OIDC wiring (the scoring
`userIDFunc` hook is where a session identity plugs in).
