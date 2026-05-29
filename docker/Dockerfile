# syntax=docker/dockerfile:1.7

ARG SERVICE
ARG GO_VERSION=1.25

FROM golang:${GO_VERSION}-alpine AS build
ARG SERVICE
WORKDIR /src

RUN apk add --no-cache git

COPY pkg ./pkg
COPY gen ./gen
COPY services/${SERVICE} ./services/${SERVICE}

# go.work is intentionally NOT copied: per-service go.mod files have
# `replace` directives that point at ../../pkg and ../../gen, so each
# service compiles standalone. GOWORK=off ensures any workspace file
# left over in a higher dir is ignored.
ENV GOWORK=off

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd services/${SERVICE} && \
    CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags='-s -w' \
      -o /out/app ./cmd/${SERVICE}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
