# Step 1: Build stage
FROM golang:1.21-alpine AS builder

# Install build dependencies for CGO (if needed) and protoc tools
RUN apk add --no-cache git protobuf-dev

WORKDIR /app

# Copy go mod files first to leverage Docker cache
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o user-app ./main.go

# Step 2: Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from the builder
COPY --from=builder /app/user-app .

# Expose ports for gRPC and HTTP Gateway
EXPOSE 50051
EXPOSE 8080

# Run the binary
CMD ["./user-app"]