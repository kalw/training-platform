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

.PHONY: all build test vet lint tidy run clean release-build image

all: vet test build

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/training

test:
	go test ./... -race -count=1

vet:
	go vet ./...

tidy:
	go mod tidy

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
	docker buildx build --platform linux/amd64,linux/arm64 \
	  --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) \
	  -t $(IMG) .

clean:
	rm -rf bin dist
