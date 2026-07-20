# Deploy (Kubernetes only)

A Helm chart is in
[`deploy/helm/training-platform`](../deploy/helm/training-platform):

```sh
helm install training deploy/helm/training-platform \
  --set serve.salt=$CTFD_SALT \
  --set ingress.enabled=true --set ingress.host=training.example.com \
  --set persistence.enabled=true          # durable solves (recommended)
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
