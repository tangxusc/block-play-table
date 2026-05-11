GO ?= go
GO_TEST_ENV ?=
DOCKER ?= docker
MANAGER_COVER_PKGS = $(shell $(GO_TEST_ENV) $(GO) list ./manager/internal/... | grep -v '/manager/internal/graph')
MANAGER_COVER_PKGS_CSV = $(shell echo $(MANAGER_COVER_PKGS) | tr ' ' ',')
MANAGER_COVER_TEST_PKGS = $(MANAGER_COVER_PKGS)

.PHONY: test test-go coverage build docker-build e2e ui-build ui-typecheck run-local stop-local clean-local

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

ui-typecheck:
	npm run typecheck

ui-build:
	npm run build

docker-build:
	$(DOCKER) build -f Dockerfile.manager -t block-play-table-manager .
	$(DOCKER) build -f Dockerfile.worker -t block-play-table-worker .
	$(DOCKER) build -f ui/Dockerfile -t block-play-table-ui .

e2e:
	$(GO_TEST_ENV) $(GO) test ./manager/e2e -count=1

run-local:
	npm run run-local

stop-local:
	npm run stop-local

clean-local:
	npm run clean-local
