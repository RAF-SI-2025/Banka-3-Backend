# Use 1.26 to match your go.mod requirement
FROM golang:1.26.1-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

# Copy everything first to ensure all local packages are available
COPY . .

# Clean up and download dependencies
RUN go mod tidy && go mod download

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o user-app ./main.go

# Stage 2: Final image
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/user-app .

EXPOSE 50051
EXPOSE 8080

CMD ["./user-app"]
