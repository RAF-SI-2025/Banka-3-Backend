FROM golang:1.25-alpine AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /bank ./cmd/bank

FROM alpine:3.19

COPY --from=build /bank /bank

EXPOSE 50051

CMD ["/bank"]
