# syntax=docker/dockerfile:1

# Keep the frontend toolchain out of the runtime image. Exact Node and npm
# versions make the embedded asset input reproducible across release builders.
FROM node:22.14.0-bookworm AS web-build
ENV TZ=UTC
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm install --global npm@10.9.2 \
    && test "$(node --version)" = "v22.14.0" \
    && test "$(npm --version)" = "10.9.2" \
    && npm ci --ignore-scripts --include=dev
COPY web/ ./
RUN npm run build \
    && test -z "$(find dist -type f -name '*.map' -print -quit)"

FROM golang:1.23-bookworm AS build

ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT_SHA=unknown
ARG BUILD_DATE=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The context excludes web/dist so stale local assets can never win over the
# deterministic build above.
COPY --from=web-build /web/dist ./web/dist

# modernc.org/sqlite is pure Go, so CGO can remain disabled and both binaries
# are statically linked for the target selected by BuildKit.
ENV CGO_ENABLED=0
RUN mkdir -p /out/data /out/config \
    && GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" go build -trimpath -buildvcs=false \
       -ldflags "-s -w -X github.com/pestit/9gateway/internal/version.Version=${VERSION} -X github.com/pestit/9gateway/internal/version.CommitSHA=${COMMIT_SHA} -X github.com/pestit/9gateway/internal/version.BuildDate=${BUILD_DATE}" \
       -o /out/gateway ./cmd/gateway \
    && GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" go build -trimpath -buildvcs=false \
       -ldflags "-s -w -X github.com/pestit/9gateway/internal/version.Version=${VERSION} -X github.com/pestit/9gateway/internal/version.CommitSHA=${COMMIT_SHA} -X github.com/pestit/9gateway/internal/version.BuildDate=${BUILD_DATE}" \
       -o /out/gwctl ./cmd/gwctl \
    && chown 65532:65532 /out/data /out/config

# Distroless static includes only the runtime files needed here, including CA
# certificates and zoneinfo. The nonroot variant uses UID/GID 65532.
FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
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
