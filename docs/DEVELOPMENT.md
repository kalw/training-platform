# Development

Building, testing, and the local dev loop on kind / k3s.

## CLI

```
training serve    [flags]   run the composed platform on one port (default :8080)
training build    [flags]   render Markdown lessons -> HTML + challenges.json
training shim     [flags]   run only the Docker-API → Kubernetes shim (default :2375)
training router   [flags]   run only the exposed-port router (default :8090)
training version            print build info
```

`serve` mounts everything: lessons at `/`, scoring at `/api/v1/challenges/`,
terminals at `/terminals/{pod}`, and (with `--enable-shim`, on by default)
the Docker API under `/docker/`. Flags mirror env vars (`LESSONS_DIR`,
`INSTANCE_IMAGE`, `ENABLE_SHIM`, `CTFD_SALT`, `SOLVES_FILE`, `ROUTER_HOST`, …).

## Build

Pure Go, no cgo — cross-compiles to every target with no C toolchain.

```sh
make build           # ./bin/training for the host
make test            # go vet + go test -race
make release-build   # dist/ binaries for linux/darwin/windows × amd64/arm64
make image           # multi-arch container (needs buildx)
make assets          # refresh vendored front-end assets from the npm pins
```

CI ([`ci.yml`](../.github/workflows/ci.yml)) runs vet + race tests + a gofmt
check, cross-compiles the full OS/arch matrix on every push and PR, runs the
Playwright e2e suite in Docker, builds a multi-arch
(`linux/amd64,linux/arm64`) image to GHCR on `main`/tags, and cuts a
goreleaser release (binaries for all targets + checksums) on `v*` tags.

## Testing

```sh
make test                              # Go: vet + race unit/integration tests
docker build -f e2e/Dockerfile -t training-e2e . && docker run --rm training-e2e
```

The end-to-end tests are [Playwright](../e2e) and run **fully self-contained
in Docker** — no local Node or Chrome needed, no Kubernetes cluster required.
They cover the lesson UI (Markdown rendering), the quiz submitted-only
behaviour, forged API calls (correct quiz hash / exercise screenshot posted
directly), and the scoreboard. The terminal spec is cluster-gated: run it
against a real cluster with `E2E_CLUSTER=1` (see below).

## Run locally on kind — the whole loop is four make targets

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
uses [`deploy/helm/dev-values.yaml`](../deploy/helm/dev-values.yaml): local
`training-platform:dev` image, lessons from the `lessons` ConfigMap mounted
at `/lessons`, a 5m idle TTL, plain-HTTP cookies, durable solves on a small
PVC, and `routerHost: direct.127.0.0.1.sslip.io:8080` — so `{:data-port=}`
links work straight through `make dev-forward`: the sslip.io wildcard
resolves to 127.0.0.1, the port-forward carries the request in, and `serve`
proxies to the Pod IP encoded in the hostname (needs internet DNS for
sslip.io). The restart on `dev-lessons` is what re-seeds the challenge store
from `challenges.json` (the ConfigMap alone propagates files, not
challenges). The `assets/` subdir of a built site is not in the ConfigMap
(ConfigMaps are flat) — the binary serves `/assets/` from its embedded copy.

> After a Docker Desktop restart the kind node container may be stopped:
> `docker start training-control-plane`.

## k3s

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

## Regenerating the README demo

`docs/demo.svg` is one self-contained animated SVG with three timed slides:
the terminal cast (a real `build` + `serve` run), then the lesson page, then
the scoreboard. The screenshots hold for **half** the cast's duration each —
they're single stills, so they read instantly, while the cast needs its full
length to type through. That also keeps the master loop a whole multiple of
the cast duration (2×), which matters: the cast has its own infinite
animation, and only a whole-multiple loop keeps the two phase-aligned
forever. Slide weights live in `WEIGHTS` in the composer. Inputs live in
`docs/` (`demo.cast`, `shot-lesson.png`, `shot-scoreboard.png`); the
screenshots are headless-Chrome captures of the served example site (poll
for the output file, then kill Chrome — it doesn't exit on its own).

```sh
# 1. record the cast (asciinema 3 writes v3; svg-term needs v2)
asciinema rec docs/demo.cast --window-size 90x12 --command "<build+serve demo>"
asciinema convert --output-format asciicast-v2 docs/demo.cast /tmp/demo-v2.cast
npx svg-term-cli --in /tmp/demo-v2.cast --out /tmp/demo-term.svg --window --padding 14

# 2. shrink the screenshots and compose the slides
sips -Z 1936 -s format jpeg -s formatOptions 70 docs/shot-lesson.png     --out /tmp/lesson.jpg
sips -Z 1936 -s format jpeg -s formatOptions 70 docs/shot-scoreboard.png --out /tmp/scoreboard.jpg
scripts/compose-demo.py /tmp/demo-term.svg /tmp/lesson.jpg /tmp/scoreboard.jpg docs/demo.svg
```
