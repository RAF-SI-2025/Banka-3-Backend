# --- build stage ---
FROM golang:1.25-alpine AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o notification ./cmd/notification


# --- runtime stage ---
FROM alpine:3.19

WORKDIR /app

COPY --from=build /app/notification ./notification
COPY --from=build /app/internal/notification/templates ./templates

EXPOSE 50051

CMD ["./notification"]
