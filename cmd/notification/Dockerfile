FROM golang:1.25-alpine AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -o /notification ./cmd/notification

FROM alpine:3.19

COPY --from=build /notification /notification
COPY --from=build /app/internal/notification/templates /templates

WORKDIR /

EXPOSE 50051

CMD ["/notification"]
