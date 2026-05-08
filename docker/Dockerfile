# syntax=docker/dockerfile:1.7

ARG SERVICE
ARG GO_VERSION=1.25

FROM golang:${GO_VERSION}-alpine AS build
ARG SERVICE
WORKDIR /src

RUN apk add --no-cache git

COPY go.work go.work.sum* ./
COPY pkg ./pkg
COPY gen ./gen
COPY services/${SERVICE} ./services/${SERVICE}

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd services/${SERVICE} && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' \
      -o /out/app ./cmd/${SERVICE}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
