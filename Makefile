GO ?= go
GO_TEST_ENV ?= GOTOOLCHAIN=local
DOCKER ?= docker
DOCKER_COMPOSE ?= $(DOCKER) compose
LOCAL_COMPOSE_FILE ?= docker-compose.team.yml
LOCAL_COMPOSE_PROJECT ?= block-play-table-local
POSTGRES_PASSWORD ?= password
WORKER_TOKEN ?= dev-worker-token
WORKER_ID ?= worker-local
WORKER_NAME ?= local-worker
WORKER_SUPPORTED_AGENTS ?= codex,claude
WORKER_DATA_SOURCE ?= ./worker-data
WORKER_SSH_DIR ?= $(HOME)/.ssh
WORKER_SSH_AUTH_SOCK ?= /run/host-services/ssh-auth.sock
LOCAL_COMPOSE_ENV = POSTGRES_PASSWORD='$(POSTGRES_PASSWORD)' WORKER_TOKEN='$(WORKER_TOKEN)' WORKER_ID='$(WORKER_ID)' WORKER_NAME='$(WORKER_NAME)' WORKER_SUPPORTED_AGENTS='$(WORKER_SUPPORTED_AGENTS)' WORKER_DATA_SOURCE='$(WORKER_DATA_SOURCE)' WORKER_SSH_DIR='$(WORKER_SSH_DIR)' WORKER_SSH_AUTH_SOCK='$(WORKER_SSH_AUTH_SOCK)'
LOCAL_COMPOSE = $(LOCAL_COMPOSE_ENV) $(DOCKER_COMPOSE) -p $(LOCAL_COMPOSE_PROJECT) -f $(LOCAL_COMPOSE_FILE)
MANAGER_COVER_PKGS = $(shell $(GO_TEST_ENV) $(GO) list ./manager/internal/... | grep -v '/manager/internal/graph')
MANAGER_COVER_PKGS_CSV = $(shell echo $(MANAGER_COVER_PKGS) | tr ' ' ',')
MANAGER_COVER_TEST_PKGS = $(MANAGER_COVER_PKGS)

.PHONY: test test-go coverage build docker-build e2e flutter-test run-local stop-local clean-local

test: test-go

test-go:
	$(GO_TEST_ENV) $(GO) test ./...

coverage:
	$(GO_TEST_ENV) $(GO) test $(MANAGER_COVER_TEST_PKGS) -coverprofile=coverage-manager.out
	$(GO_TEST_ENV) $(GO) tool cover -func=coverage-manager.out | tee coverage-manager.txt
	@awk '/^total:/ { split($$3, pct, "%"); if (pct[1] < 80) { printf("manager coverage %.1f%% is below 80%%\n", pct[1]); exit 1 } }' coverage-manager.txt
	$(GO_TEST_ENV) $(GO) test ./manager/e2e
	$(GO_TEST_ENV) $(GO) test ./worker/internal/... -coverprofile=coverage-worker.out
	$(GO_TEST_ENV) $(GO) tool cover -func=coverage-worker.out | tee coverage-worker.txt
	@awk '/^total:/ { split($$3, pct, "%"); if (pct[1] < 80) { printf("worker coverage %.1f%% is below 80%%\n", pct[1]); exit 1 } }' coverage-worker.txt
	$(GO_TEST_ENV) $(GO) test ./pkg/... -coverprofile=coverage-pkg.out
	$(GO_TEST_ENV) $(GO) tool cover -func=coverage-pkg.out | tee coverage-pkg.txt
	@awk '/^total:/ { split($$3, pct, "%"); if (pct[1] < 80) { printf("pkg coverage %.1f%% is below 80%%\n", pct[1]); exit 1 } }' coverage-pkg.txt
	@cat coverage-manager.txt coverage-worker.txt coverage-pkg.txt > coverage.txt

build:
	$(GO_TEST_ENV) $(GO) build ./manager/cmd/manager ./worker/cmd/worker

flutter-test:
	$(DOCKER) build -f ui/Dockerfile --target builder .

docker-build:
	$(DOCKER) build -f Dockerfile.manager -t block-play-table-manager .
	$(DOCKER) build -f Dockerfile.worker -t block-play-table-worker .
	$(DOCKER) build -f ui/Dockerfile -t block-play-table-ui .

e2e:
	$(GO_TEST_ENV) $(GO) test ./manager/e2e -count=1

run-local:
	mkdir -p '$(WORKER_DATA_SOURCE)'
	$(LOCAL_COMPOSE) up --build -d postgres manager worker ui

stop-local:
	$(LOCAL_COMPOSE) down --remove-orphans

clean-local:
	$(LOCAL_COMPOSE) down -v --remove-orphans
	rm -rf '$(WORKER_DATA_SOURCE)'
