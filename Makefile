GO ?= go
GO_TEST_ENV ?= GOTOOLCHAIN=local

.PHONY: test test-go coverage build docker-build e2e flutter-test

test: test-go

test-go:
	$(GO_TEST_ENV) $(GO) test ./...

coverage:
	$(GO_TEST_ENV) $(GO) test ./manager/internal/... -coverprofile=coverage-manager.out
	$(GO_TEST_ENV) $(GO) tool cover -func=coverage-manager.out | tee coverage-manager.txt
	@awk '/^total:/ { split($$3, pct, "%"); if (pct[1] < 80) { printf("manager coverage %.1f%% is below 80%%\n", pct[1]); exit 1 } }' coverage-manager.txt
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
	docker build -f ui/Dockerfile --target builder .

docker-build:
	docker build -f Dockerfile.manager -t block-play-table-manager .
	docker build -f Dockerfile.worker -t block-play-table-worker .
	docker build -f ui/Dockerfile -t block-play-table-ui .

e2e:
	$(GO_TEST_ENV) $(GO) test ./manager/e2e -count=1
