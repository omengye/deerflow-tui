# Go build and packaging targets for deerflow-tui.
#
# Common commands:
#   make test
#   make build
#   make package VERSION=v1.0.0
#   make clean

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := package

APP_NAME := deerflow-tui
CMD := ./cmd/deerflow-tui

VERSION ?= dev
GO ?= go
GOARCH := amd64
CGO_ENABLED ?= 0

DIST_DIR := dist
BUILD_DIR := $(DIST_DIR)/build
STAGE_DIR := $(DIST_DIR)/stage
PACKAGE_DIR := $(DIST_DIR)/packages

GO_BUILD_FLAGS := -trimpath
LD_FLAGS := -s -w -X main.Version=$(VERSION)

LINUX_NAME := $(APP_NAME)-$(VERSION)-linux-amd64
WINDOWS_NAME := $(APP_NAME)-$(VERSION)-windows-amd64

.PHONY: help test run build build-linux build-windows package package-linux package-windows checksums clean

help:
	@printf '%s\n' \
		'Targets:' \
		'  make test                 Run Go tests' \
		'  make run                  Run the TUI locally' \
		'  make build                Build linux/windows amd64 binaries' \
		'  make package VERSION=vX.Y  Build tar.gz/zip packages and SHA256SUMS' \
		'  make clean                Remove generated dist files'

test:
	$(GO) test ./...

run:
	$(GO) run $(CMD)

build: build-linux build-windows

build-linux:
	@mkdir -p "$(BUILD_DIR)/linux-amd64"
	CGO_ENABLED=$(CGO_ENABLED) GOOS=linux GOARCH=$(GOARCH) \
		$(GO) build $(GO_BUILD_FLAGS) -ldflags "$(LD_FLAGS)" \
		-o "$(BUILD_DIR)/linux-amd64/$(APP_NAME)" "$(CMD)"

build-windows:
	@mkdir -p "$(BUILD_DIR)/windows-amd64"
	CGO_ENABLED=$(CGO_ENABLED) GOOS=windows GOARCH=$(GOARCH) \
		$(GO) build $(GO_BUILD_FLAGS) -ldflags "$(LD_FLAGS)" \
		-o "$(BUILD_DIR)/windows-amd64/$(APP_NAME).exe" "$(CMD)"

package: checksums

package-linux: build-linux
	@mkdir -p "$(PACKAGE_DIR)"
	@stage="$(STAGE_DIR)/$(LINUX_NAME)"; \
		rm -rf "$$stage"; \
		mkdir -p "$$stage"; \
		cp "$(BUILD_DIR)/linux-amd64/$(APP_NAME)" "$$stage/"; \
		cp README.md LICENSE .env.example "$$stage/"; \
		tar -C "$(STAGE_DIR)" -czf "$(PACKAGE_DIR)/$(LINUX_NAME).tar.gz" "$(LINUX_NAME)"; \
		echo "Created $(PACKAGE_DIR)/$(LINUX_NAME).tar.gz"

package-windows: build-windows
	@command -v zip >/dev/null || { echo "zip is required for Windows packages" >&2; exit 1; }
	@mkdir -p "$(PACKAGE_DIR)"
	@stage="$(STAGE_DIR)/$(WINDOWS_NAME)"; \
		rm -rf "$$stage"; \
		mkdir -p "$$stage"; \
		cp "$(BUILD_DIR)/windows-amd64/$(APP_NAME).exe" "$$stage/"; \
		cp README.md LICENSE .env.example "$$stage/"; \
		(cd "$(STAGE_DIR)" && zip -qr "../packages/$(WINDOWS_NAME).zip" "$(WINDOWS_NAME)"); \
		echo "Created $(PACKAGE_DIR)/$(WINDOWS_NAME).zip"

checksums: package-linux package-windows
	@cd "$(PACKAGE_DIR)" && sha256sum *.tar.gz *.zip > SHA256SUMS
	@echo "Created $(PACKAGE_DIR)/SHA256SUMS"

clean:
	rm -rf "$(DIST_DIR)"
