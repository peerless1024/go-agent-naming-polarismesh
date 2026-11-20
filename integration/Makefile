SHELL := /bin/bash
.DEFAULT_GOAL := build

ROOT_DIR := $(abspath $(CURDIR)/..)
GO ?= go
TARGET ?= $(shell $(GO) env GOOS)/$(shell $(GO) env GOARCH)
TARGET_OS := $(firstword $(subst /, ,$(TARGET)))
TARGET_ARCH := $(word 2,$(subst /, ,$(TARGET)))
TARGET_DIR := $(TARGET_OS)-$(TARGET_ARCH)
BIN_DIR ?= $(CURDIR)/bin/$(TARGET_DIR)
GOCACHE ?= /tmp/go-agent-naming-package-cache-$(TARGET_DIR)
LDFLAGS ?= -s -w

BUILD_ENV = CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) GOCACHE=$(GOCACHE)

.PHONY: help
help:
	@printf '%s\n' \
		'Usage:' \
		'  make -C integration build [TARGET=linux/amd64]' \
		'  make -C integration package [TARGET=linux/amd64]' \
		'  make -C integration verify [TARGET=linux/amd64] POLARIS_SERVER=host' \
		'  make -C integration clean'

.PHONY: build
build: clean-bin
	mkdir -p "$(BIN_DIR)"
	cd "$(ROOT_DIR)" && $(BUILD_ENV) $(GO) test -c -mod=readonly -tags=integration \
		-trimpath -ldflags="$(LDFLAGS)" -o "$(BIN_DIR)/a2a.test" ./integration/a2a
	cd "$(ROOT_DIR)" && $(BUILD_ENV) $(GO) test -c -mod=readonly -tags=integration \
		-trimpath -ldflags="$(LDFLAGS)" -o "$(BIN_DIR)/mcp.test" ./integration/mcp
	cd "$(ROOT_DIR)" && $(BUILD_ENV) $(GO) test -c -mod=readonly -tags=integration \
		-trimpath -ldflags="$(LDFLAGS)" -o "$(BIN_DIR)/skill.test" ./integration/skill
	cd "$(ROOT_DIR)" && $(BUILD_ENV) $(GO) test -c -mod=readonly -tags=integration \
		-trimpath -ldflags="$(LDFLAGS)" -o "$(BIN_DIR)/a2a-agentcard.test" ./integration/a2a-agentcard
	cd "$(ROOT_DIR)" && $(BUILD_ENV) $(GO) test -c -mod=readonly -tags=integration \
		-trimpath -ldflags="$(LDFLAGS)" -o "$(BIN_DIR)/harness.test" ./integration/internal/harness
	cd "$(ROOT_DIR)" && $(BUILD_ENV) $(GO) build -mod=readonly \
		-trimpath -ldflags="$(LDFLAGS)" -o "$(BIN_DIR)/agentcard-provider" ./examples/a2a-agentcard/provider
	cd "$(ROOT_DIR)" && $(BUILD_ENV) $(GO) build -mod=readonly \
		-trimpath -ldflags="$(LDFLAGS)" -o "$(BIN_DIR)/agentcard-client" ./examples/a2a-agentcard/client

.PHONY: package
package:
	./build-materials.sh --target="$(TARGET)"

.PHONY: verify
verify: build
	POLARIS_SERVER="$(POLARIS_SERVER)" ./verify_all.sh --bin-dir="$(BIN_DIR)"

.PHONY: clean-bin
clean-bin:
	rm -rf "$(BIN_DIR)"

.PHONY: clean
clean: clean-bin
	rm -rf "$(CURDIR)/dist"
