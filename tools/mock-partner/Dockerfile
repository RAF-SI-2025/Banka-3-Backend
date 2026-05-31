# syntax=docker/dockerfile:1.7
#
# Standalone build for the celina-5 mock-partner. Self-contained
# (no pkg/ or gen/ imports) so it lives outside services/ + reuses a
# vanilla stdlib build.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY tools/mock-partner/go.mod ./go.mod
COPY tools/mock-partner/main.go ./main.go
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags='-s -w' \
      -o /out/mock-partner .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mock-partner /mock-partner
USER nonroot:nonroot
EXPOSE 9099
ENTRYPOINT ["/mock-partner"]
