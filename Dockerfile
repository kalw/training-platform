# syntax=docker/dockerfile:1
# Multi-arch build: `docker buildx build --platform linux/amd64,linux/arm64`.
# TARGETOS/TARGETARCH are set by buildx per target platform; the build is
# pure Go (CGO disabled) so cross-compilation needs no C toolchain.
FROM --platform=$BUILDPLATFORM golang:1.23 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -ldflags "-s -w \
      -X github.com/kalw/training-platform/internal/version.Version=$VERSION \
      -X github.com/kalw/training-platform/internal/version.Commit=$COMMIT \
      -X github.com/kalw/training-platform/internal/version.Date=$DATE" \
    -o /out/training ./cmd/training

# Distroless static: no shell, no package manager, runs as nonroot.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/training /usr/local/bin/training
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["training"]
CMD ["serve"]
