FROM golang:1.25-alpine AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o gateway ./cmd/gateway

FROM alpine:3.19

WORKDIR /app
COPY --from=build /app/gateway ./gateway

EXPOSE 8080

CMD ["./gateway"]