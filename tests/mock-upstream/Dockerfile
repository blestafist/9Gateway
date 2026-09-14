# syntax=docker/dockerfile:1

FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /out/mock-upstream .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mock-upstream /mock-upstream
USER 65532:65532
EXPOSE 8081
ENTRYPOINT ["/mock-upstream"]
