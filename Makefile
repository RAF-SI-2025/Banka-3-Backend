-include .env
export

GO_IMAGE := golang:1.25
GO_RUN   := docker run --rm -v $(PWD):/app -w /app $(GO_IMAGE)

ADMIN_EMAIL  ?= admin@banka.raf
CLIENT_EMAIL ?= petar@primer.raf

.PHONY: all up down down-v proto schema seed nuke lint lint-l build build-l test test-l test-integration test-integration-l fmt fmt-l

all: proto up

up:
	docker compose up -d --build

down:
	docker compose down

down-v:
	docker compose down -v

proto:
	docker build -t banka-proto -f scripts/proto/Dockerfile .
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -Command "$$protoRoot = Join-Path (Get-Location) 'proto'; $$workspace = (Get-Location).Path -replace '\\','/'; $$mount = $$workspace + ':/workspace'; $$protoFiles = Get-ChildItem -Path $$protoRoot -Recurse -Filter *.proto -File | ForEach-Object { $$_.FullName.Substring($$protoRoot.Length + 1).Replace('\', '/') }; if (-not $$protoFiles) { throw 'No proto files found under proto/'; }; $$env:MSYS_NO_PATHCONV = '1'; $$env:MSYS2_ARG_CONV_EXCL = '*'; $$dockerArgs = @('run', '--rm', '-v', $$mount, 'banka-proto', '--proto_path=/workspace/proto', '--go_out=/workspace/gen', '--go_opt=paths=source_relative', '--go-grpc_out=/workspace/gen', '--go-grpc_opt=paths=source_relative') + $$protoFiles; & docker @dockerArgs"
else
	docker run --rm -v "$(PWD):/workspace" -u $$(id -u):$$(id -g) banka-proto \
		--proto_path=/workspace/proto \
		--go_out=/workspace/gen --go_opt=paths=source_relative \
		--go-grpc_out=/workspace/gen --go-grpc_opt=paths=source_relative \
		$$(cd proto && find . -name '*.proto' | sed 's|^\./||')
endif

schema:
	docker compose exec -T postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) < scripts/db/schema.sql

seed:
	docker compose exec -T postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) \
		-v admin_email=$(ADMIN_EMAIL) -v client_email=$(CLIENT_EMAIL) \
		< scripts/db/seed.sql

nuke:
	docker compose exec -T postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"

lint:
	docker run --rm -v $(PWD):/app -w /app golangci/golangci-lint:v2.4 golangci-lint run ./...

lint-l:
	golangci-lint run ./...

build:
	$(GO_RUN) go build ./cmd/...

build-l:
	go build ./cmd/...

test:
	$(GO_RUN) go test -race -count=1 -tags=integration ./...

test-l:
	go test -race -count=1 -tags=integration ./...

fmt:
	$(GO_RUN) gofmt -l -w .

fmt-l:
	gofmt -l -w .
