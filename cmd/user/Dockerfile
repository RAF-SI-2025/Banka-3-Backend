# --- build stage ---
FROM golang:1.25-alpine AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o user-service ./cmd/user


# --- runtime stage ---
FROM alpine:3.19

WORKDIR /app

COPY --from=build /app/user-service .

EXPOSE 50051

CMD ["./user-service"]
