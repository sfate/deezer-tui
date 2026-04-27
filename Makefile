.PHONY: lint audit test build ci

GO_DIR ?= go
BINARY_NAME ?= deezer-tui
BUILD_OUTPUT ?= target/release/$(BINARY_NAME)
TEST_DIR ?= ./...
TEST_CASE ?= ^.+$

lint:
	go -C $(GO_DIR) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.4.0 run --allow-parallel-runners

audit:
	go -C $(GO_DIR) run golang.org/x/vuln/cmd/govulncheck@latest ./...

test:
	go -C $(GO_DIR) test -mod=readonly -count=1 -p 1 -failfast -race -run $(TEST_CASE) $(TEST_DIR)

build:
	mkdir -p $(dir $(BUILD_OUTPUT))
	go -C $(GO_DIR) build -mod=readonly -o ../$(BUILD_OUTPUT) ./cmd/deezer-tui

ci: lint audit test
