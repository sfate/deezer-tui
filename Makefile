.PHONY: lint audit test build ci

BINARY_NAME ?= deezer-tui
BUILD_OUTPUT ?= bin/$(BINARY_NAME)
TEST_DIR ?= ./...
TEST_CASE ?= ^.+$
UNAME_S := $(shell uname -s)
MAC_HELPER_SRC := internal/tui/mac_player_helper.swift
MAC_HELPER_CACHE_DIR ?= $(HOME)/Library/Caches/deezer-tui
MAC_HELPER_SWIFT := $(MAC_HELPER_CACHE_DIR)/mac_player_helper.swift
MAC_HELPER_BIN := $(MAC_HELPER_CACHE_DIR)/mac-player-helper
MAC_HELPER_SUM := $(MAC_HELPER_CACHE_DIR)/mac-player-helper.sha256

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.4.0 run --allow-parallel-runners

audit:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

test:
	go test -mod=readonly -count=1 -p 1 -failfast -race -run $(TEST_CASE) $(TEST_DIR)

build:
	mkdir -p $(dir $(BUILD_OUTPUT))
ifeq ($(UNAME_S),Darwin)
	mkdir -p $(MAC_HELPER_CACHE_DIR)
	cp $(MAC_HELPER_SRC) $(MAC_HELPER_SWIFT)
	xcrun swiftc -O -o $(MAC_HELPER_BIN) $(MAC_HELPER_SWIFT)
	printf "%s" "$$(shasum -a 256 $(MAC_HELPER_SRC) | awk '{print $$1}')" > $(MAC_HELPER_SUM)
endif
	go build -mod=readonly -o $(BUILD_OUTPUT) ./cmd/deezer-tui

ci: lint audit test
