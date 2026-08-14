GO ?= go
GO_TEST_ENV ?=
DOCKER ?= docker
.PHONY: test test-go coverage coverage-go coverage-branch coverage-ui fuzz build docker-build e2e ui-build ui-typecheck run-local stop-local clean-local

test: test-go

test-go:
	$(GO_TEST_ENV) $(GO) test ./...

coverage: coverage-go coverage-branch coverage-ui

coverage-go:
	GO="$(GO)" scripts/go_statement_coverage.sh

coverage-branch:
	GO="$(GO)" scripts/go_branch_coverage.sh

coverage-ui:
	npm run coverage:ui

fuzz:
	GO="$(GO)" scripts/go_fuzz.sh

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
