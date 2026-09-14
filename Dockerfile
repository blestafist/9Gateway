# syntax=docker/dockerfile:1

FROM golang:1.23-bookworm AS build

ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=0.1.0
ARG COMMIT_SHA=unknown
ARG BUILD_DATE=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# modernc.org/sqlite is pure Go, so CGO can remain disabled and both binaries
# are statically linked for the target selected by BuildKit.
ENV CGO_ENABLED=0
RUN mkdir -p /out/data /out/config \
    && GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" go build -trimpath -buildvcs=false \
       -ldflags "-s -w -X github.com/pestit/9gateway/internal/gwctl.Version=${VERSION}" \
       -o /out/gateway ./cmd/gateway \
    && GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" go build -trimpath -buildvcs=false \
       -ldflags "-s -w -X github.com/pestit/9gateway/internal/gwctl.Version=${VERSION}" \
       -o /out/gwctl ./cmd/gwctl \
    && chown 65532:65532 /out/data /out/config

# Distroless static includes only the runtime files needed here, including CA
# certificates and zoneinfo. The nonroot variant uses UID/GID 65532.
FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=0.1.0
ARG COMMIT_SHA=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="9gateway" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT_SHA}" \
      org.opencontainers.image.created="${BUILD_DATE}"

COPY --from=build --chown=65532:65532 /out/gateway /gateway
COPY --from=build --chown=65532:65532 /out/gwctl /gwctl
ENV PATH=/:/usr/local/bin:/usr/local/sbin:/usr/bin:/sbin:/bin
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build --chown=65532:65532 /out/config /etc/gateway

USER 65532:65532
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=3s CMD ["/gateway", "healthcheck"]
ENTRYPOINT ["/gateway"]
CMD ["--config", "/etc/gateway/config.yaml"]
