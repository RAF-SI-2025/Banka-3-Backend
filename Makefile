-include .env
export

GO_IMAGE := golang:1.25
GO_RUN   := docker run --rm -v $(PWD):/app -w /app $(GO_IMAGE)
SPARK_ANALYTICS_IMAGE ?= banka-analytics-spark:latest

ADMIN_EMAIL  ?= admin@banka.raf
CLIENT_EMAIL ?= petar@primer.raf

SERVICES := bank exchange gateway notification user
PACKAGES := pkg
NAMES    := $(SERVICES) $(PACKAGES)

MODULES := pkg services/bank services/exchange services/gateway services/notification services/user

module_path = $(if $(filter $(1),$(SERVICES)),services/$(1),$(1))

TARGET     := $(filter $(NAMES),$(MAKECMDGOALS))
TARGET_DIR := $(if $(TARGET),$(foreach t,$(TARGET),$(call module_path,$(t))),$(MODULES))

# No-op targets so e.g. `make lint bank` doesn't try to build `bank`.
$(NAMES):
	@:

.PHONY: all up down down-v proto schema seed nuke refresh-partitions verify-replica verify-partitions verify-indexes verify-spark-analytics verify-spark-ml spark-analytics-image spark-analytics-local spark-ml-local k8s-gateway-image k8s-autoscaling-apply k8s-autoscaling-status lint lint-l build build-l test test-l test-integration test-integration-l fmt fmt-l $(NAMES)

all: proto up schema seed

up:
	docker compose up -d --build

down:
	docker compose down

down-v:
	docker compose down -v

proto:
	docker build -t banka-proto -f scripts/proto/Dockerfile .
	docker run --rm -v $(PWD):/workspace -u $$(id -u):$$(id -g) banka-proto \
		--proto_path=/workspace/proto \
		--go_out=/workspace/pkg/proto --go_opt=paths=source_relative \
		--go-grpc_out=/workspace/pkg/proto --go-grpc_opt=paths=source_relative \
		$$(cd proto && find . -name '*.proto' | sed 's|^\./||')

schema:
	docker compose exec -T postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) < scripts/db/schema.sql

seed:
	docker compose exec -T postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) \
		-v admin_email=$(ADMIN_EMAIL) -v client_email=$(CLIENT_EMAIL) \
		< scripts/db/seed.sql

nuke:
	docker compose exec -T postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"

refresh-partitions:
	docker compose exec -T postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) < scripts/db/refresh_partitions.sql

verify-replica:
	docker compose exec -T postgres_replica psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) < scripts/db/verify_replica.sql

verify-partitions:
	docker compose exec -T postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) < scripts/db/verify_partitions.sql

verify-indexes:
	docker compose exec -T postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) < scripts/db/verify_indexes.sql

verify-spark-analytics:
	docker compose exec -T postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) < scripts/db/verify_spark_analytics.sql

verify-spark-ml:
	docker compose exec -T postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) < scripts/db/verify_spark_ml.sql

spark-analytics-image:
	docker build -t $(SPARK_ANALYTICS_IMAGE) -f analytics/spark/Dockerfile .

spark-analytics-local:
	docker compose run --rm spark_analytics

spark-ml-local:
	docker compose run --rm spark_analytics /opt/spark/bin/spark-submit --master local[2] --jars /opt/spark/jars/postgresql-42.7.5.jar --conf spark.driver.extraClassPath=/opt/spark/jars/postgresql-42.7.5.jar --conf spark.executor.extraClassPath=/opt/spark/jars/postgresql-42.7.5.jar local:///opt/banka-analytics/jobs/account_activity_ml.py

k8s-gateway-image:
	docker build -t banka-3-backend-gateway:latest -f docker/Dockerfile --build-arg SERVICE=gateway .

k8s-autoscaling-apply:
	kubectl apply -k k8s/autoscaling/gateway

k8s-autoscaling-status:
	kubectl get deploy,svc,hpa,pdb -n banka-platform

lint:
	@for m in $(TARGET_DIR); do \
		echo ">>> lint $$m"; \
		docker run --rm -v $(PWD):/app -w /app/$$m golangci/golangci-lint:v2.4 golangci-lint run ./... || exit 1; \
	done

lint-l:
	@for m in $(TARGET_DIR); do \
		echo ">>> lint $$m"; \
		(cd $$m && golangci-lint run ./...) || exit 1; \
	done

build:
	@for m in $(TARGET_DIR); do \
		echo ">>> build $$m"; \
		$(GO_RUN) sh -c "cd $$m && go build ./..." || exit 1; \
	done

build-l:
	@for m in $(TARGET_DIR); do \
		echo ">>> build $$m"; \
		(cd $$m && go build ./...) || exit 1; \
	done

test:
	@for m in $(TARGET_DIR); do \
		echo ">>> test $$m"; \
		$(GO_RUN) sh -c "cd $$m && go test -race -count=1 -tags=integration ./..." || exit 1; \
	done

test-l:
	@for m in $(TARGET_DIR); do \
		echo ">>> test $$m"; \
		(cd $$m && go test -race -count=1 -tags=integration ./...) || exit 1; \
	done

fmt:
	$(GO_RUN) gofmt -l -w services/ pkg/

fmt-l:
	gofmt -l -w services/ pkg/
