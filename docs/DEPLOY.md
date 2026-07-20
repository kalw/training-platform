# Deploy (Kubernetes only)

## Install from the published chart

The chart is published to GHCR as an OCI artifact on every `v*` tag — the
same registry and credentials as the container image, so there is no
`gh-pages` branch or chart index to maintain:

```sh
helm install training oci://ghcr.io/kalw/charts/training-platform --version 0.1.0 \
  --namespace training --create-namespace \
  --set serve.salt=$CTFD_SALT \
  --set ingress.enabled=true --set ingress.host=training.example.com \
  --set persistence.enabled=true          # durable solves (recommended)
```

The chart version equals the release tag, and its `appVersion` pins the
image built from that same tag — so `--version 0.1.0` gets you the `0.1.0`
platform image by default. List what's available with:

```sh
helm show chart oci://ghcr.io/kalw/charts/training-platform --version 0.1.0
```

> Like other new GHCR packages, `charts/training-platform` is **private on
> first publish** — flip it public by hand in the GitHub UI (Package settings
> → Danger Zone), or `helm registry login ghcr.io` before installing.

## Install from a checkout

```sh
helm install training deploy/helm/training-platform \
  --set serve.salt=$CTFD_SALT \
  --set ingress.enabled=true --set ingress.host=training.example.com \
  --set persistence.enabled=true
```

It renders a Deployment (unprivileged, read-only rootfs, drops all caps), a
Service, optional Ingress (including the `*.direct.<domain>` wildcard for the
port router), and **least-privilege RBAC**: a cluster-scoped Role for
Namespace lifecycle + GC, and a namespaced Role for `pods` / `pods/exec` /
`pods/attach` / `pods/log` (+ `patch` for the session keepalive) confined to
the session namespace. Privileged session Pods (nested dockerd) are permitted
**only** in that one namespace via Pod Security Admission — never
cluster-wide.

## Values worth knowing

| value | why |
|---|---|
| `serve.salt` | must match the salt the lesson site was built with, or nothing grades |
| `serve.routerHost` | public suffix for exposed-port links; usually matches `ingress.wildcardHost` |
| `serve.sessionIdleTTL` / `serve.sessionTTL` | idle window (keepalive-slid) and hard cap for session Pods |
| `persistence.enabled` | **off by default** — solves are in-memory (lost on restart) until enabled. The PVC is annotated `helm.sh/resource-policy: keep` so `helm uninstall` doesn't bin learner progress |
| `challengesFile` | challenges.json (mount via `extraVolumes`) seeded at boot |

Lessons are typically mounted as a ConfigMap of a `training build` output —
see the dev loop in [DEVELOPMENT.md](DEVELOPMENT.md) for the exact shape.

## Runtime configuration model

Everything is configured **at container start**, not build time (the one
exception: the lessons build consumes `CTFD_SALT`). Changing a URL/domain
means restarting the container, not rebuilding the image.

## Single replica

The solve log is one append-only file on a `ReadWriteOnce` claim with a
single writer — this is a single-replica design. Scaling the Deployment past
1 needs a different persistence mechanism first.

## One release per cluster (by default)

The chart creates the session Namespace (`sessionNamespace`, default
`training-sessions`) and Helm records ownership on it, so a second release
installed with the defaults fails with an "invalid ownership metadata"
error. That's intentional — one platform per cluster is the normal case. To
run two side by side (e.g. staging next to a demo), give each its own:

```sh
--set sessionNamespace=demo-sessions
```

## Chart CI

`helm lint` (plain and `--strict`) plus `helm template` under several value
permutations — default, ingress + persistence + router, and the dev values —
run on every push and PR, so a broken template fails before it ships.
Packaging and publishing happen only on tags.
