FROM golang:1.25-alpine

RUN apk add --no-cache protobuf-dev git

RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11 && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1

WORKDIR /workspace

ENTRYPOINT ["protoc"]
