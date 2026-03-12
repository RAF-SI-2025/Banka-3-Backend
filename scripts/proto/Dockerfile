FROM golang:1.25-alpine

RUN apk add --no-cache protobuf-dev git

RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest && \
    go install github.com/grpc-ecosystem/grpc-gateway/v2/cmd/protoc-gen-grpc-gateway@latest

RUN git clone --depth 1 https://github.com/googleapis/googleapis.git /opt/googleapis

WORKDIR /workspace

ENTRYPOINT ["protoc"]
