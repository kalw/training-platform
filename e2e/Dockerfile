# Self-contained Playwright end-to-end run: builds the training binary and
# executes the browser tests inside the official Playwright image (Node +
# browsers + system deps baked in) so nothing depends on a local Node/Chrome
# install. Build context is the repo root:
#
#   docker build -f e2e/Dockerfile -t training-e2e .
#   docker run --rm training-e2e
#
# No Kubernetes cluster is needed: serve degrades sessions/terminals to 503
# without one, and the terminal spec is skipped unless E2E_CLUSTER=1.

# 1) Build the training binary with the Go toolchain.
FROM golang:1.25 AS gobuild
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# -p 1 compiles one package at a time: slower, but keeps peak memory low
# enough to build client-go on small Docker/Podman VMs without an OOM kill.
RUN CGO_ENABLED=0 go build -p 1 -o /out/training ./cmd/training

# 2) Run Playwright. Pin to the same version as e2e/package.json.
FROM mcr.microsoft.com/playwright:v1.61.1-jammy
WORKDIR /app
COPY --from=gobuild /out/training /usr/local/bin/training
COPY examples/ ./examples/
COPY e2e/ ./e2e/
WORKDIR /app/e2e
RUN npm ci --no-audit --no-fund || npm install --no-audit --no-fund

ENV TRAINING_BIN=/usr/local/bin/training
ENV CI=1
# serve.sh cd's to the repo root (/app) for `examples/lessons`.
CMD ["npx", "playwright", "test"]
