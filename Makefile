BINARY      := training
PKG         := github.com/kalw/training-platform
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

# OS/arch matrix for `make release-build`.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: all build test vet lint tidy run clean release-build image assets chart-lint chart-package

all: vet test build

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/training

test:
	go test ./... -race -count=1

vet:
	go vet ./...

tidy:
	go mod tidy

CHART := deploy/helm/training-platform

# Validate the chart exactly as CI does (lint + render the value permutations).
chart-lint:
	helm lint $(CHART)
	helm lint --strict $(CHART)
	helm template t $(CHART) > /dev/null
	helm template t $(CHART) --set ingress.enabled=true --set ingress.host=training.example.com \
	  --set persistence.enabled=true --set serve.routerHost=direct.training.example.com \
	  --set serve.salt=ci > /dev/null
	helm template t $(CHART) -f deploy/helm/dev-values.yaml > /dev/null

# Package the chart locally. CI publishes it to oci://ghcr.io/kalw/charts on tags.
chart-package:
	helm package $(CHART) --destination dist

# Refresh the vendored front-end assets (xterm.js & co) from the npm pins in
# internal/content/assets/package.json (+ lockfile, integrity-verified by
# `npm ci`). Renovate manages the pins; CI enforces the copies match.
assets:
	./scripts/vendor-assets.sh

run: build
	./bin/$(BINARY) serve

# Cross-compile every OS/arch into dist/. bin name gets .exe on windows.
release-build:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; \
	  ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
	  out=dist/$(BINARY)_$${os}_$${arch}$$ext; \
	  echo "building $$out"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o $$out ./cmd/training || exit 1; \
	done

# Local multi-arch image (needs buildx). Override IMG to push elsewhere.
IMG ?= ghcr.io/kalw/training-platform:$(VERSION)
image:
	docker-buildx build --platform linux/amd64,linux/arm64 \
	  --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) \
	  -t $(IMG) .

# ---------------------------------------------------------------------------
# Local dev loop against a kind cluster (see README "Local dev loop"):
#   make dev-deploy    everything: image + lessons + helm install/upgrade
#   make dev-image     code changed  -> rebuild image, load into kind, restart
#   make dev-lessons   lessons changed -> re-render, update ConfigMap, restart
#                      (restart re-seeds the challenge store from challenges.json)
#   make dev-forward   port-forward the platform to http://localhost:8080
#   make dev-down      uninstall the helm release
KIND_CLUSTER    ?= training
DEV_NS          ?= training
DEV_RELEASE     ?= training
LESSONS_SRC     ?= examples/lessons
DEV_SALT        ?= demo-salt
DEV_ROUTER_HOST ?=

.PHONY: dev-deploy dev-image dev-lessons dev-forward dev-down

dev-image:
	docker build -t training-platform:dev .
	kind load docker-image training-platform:dev --name $(KIND_CLUSTER)
	-kubectl -n $(DEV_NS) rollout restart deployment/$(DEV_RELEASE) 2>/dev/null

dev-lessons: build
	./bin/$(BINARY) build --src $(LESSONS_SRC) --out site --salt $(DEV_SALT)
	kubectl create namespace $(DEV_NS) --dry-run=client -o yaml | kubectl apply -f -
	kubectl -n $(DEV_NS) create configmap lessons --from-file=site/ \
	  --dry-run=client -o yaml | kubectl apply -f -
	-kubectl -n $(DEV_NS) rollout restart deployment/$(DEV_RELEASE) 2>/dev/null

dev-deploy: dev-image dev-lessons
	helm upgrade --install $(DEV_RELEASE) deploy/helm/training-platform \
	  -n $(DEV_NS) --create-namespace \
	  -f deploy/helm/dev-values.yaml \
	  --set serve.salt=$(DEV_SALT) \
	  $(if $(DEV_ROUTER_HOST),--set serve.routerHost=$(DEV_ROUTER_HOST),)
	kubectl -n $(DEV_NS) rollout status deployment/$(DEV_RELEASE) --timeout=120s

dev-forward:
	kubectl -n $(DEV_NS) port-forward svc/$(DEV_RELEASE) 8080:8080

dev-down:
	-helm -n $(DEV_NS) uninstall $(DEV_RELEASE)

clean:
	rm -rf bin dist
