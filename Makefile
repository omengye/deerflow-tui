# Linux SEA build for deerflow-tui
#
# Why this Makefile exists:
#   Distro-shipped node binaries (apt/dnf/snap/strip-ed images) often have
#   non-standard ELF section layouts. postject's blob injection then corrupts
#   .dynamic/.rela.* and the binary segfaults inside ld-linux's
#   _dl_relocate_object before main() runs. Building against the official
#   nodejs.org tarball avoids that.
#
# Usage:
#   make                  # full build: download node + bundle + blob + inject
#   make run              # build then run ./dist-sea/deerflow-tui
#   make clean            # remove build artifacts (keep downloaded node)
#   make distclean        # also remove .cache/node-*
#   make NODE_VERSION=v24.14.0
#   make NODE_ARCH=arm64

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := package

NODE_VERSION ?= v24.14.0
NODE_ARCH    ?= x64
NODE_PLATFORM = linux-$(NODE_ARCH)
NODE_TARBALL  = node-$(NODE_VERSION)-$(NODE_PLATFORM).tar.xz
NODE_URL      = https://nodejs.org/dist/$(NODE_VERSION)/$(NODE_TARBALL)

CACHE_DIR    := .cache
NODE_DIR     := $(CACHE_DIR)/node-$(NODE_VERSION)-$(NODE_PLATFORM)
NODE_BIN     := $(abspath $(NODE_DIR)/bin/node)

OUT_DIR      := dist-sea
BIN_NAME     := deerflow-tui
BUNDLE       := $(OUT_DIR)/entry.mjs
BLOB         := $(OUT_DIR)/$(BIN_NAME).blob
TARGET_BIN   := $(OUT_DIR)/$(BIN_NAME)
POSTJECT     := node_modules/.bin/postject
SEA_FUSE     := NODE_SEA_FUSE_fce680ab2cc467b6e072b8b5df1996b2

.PHONY: package run check node deps bundle blob inject clean distclean

# Top-level: builds the SEA binary
package: $(TARGET_BIN)
	@echo
	@echo "✔ Built $(TARGET_BIN)"
	@ls -lh $(TARGET_BIN)

run: package
	@./$(TARGET_BIN)

# 1. Download official node tarball
$(NODE_BIN):
	@echo "→ Fetching $(NODE_TARBALL)"
	@mkdir -p $(CACHE_DIR)
	@if ! command -v curl >/dev/null; then \
		echo "curl required" >&2; exit 1; \
	fi
	@curl -fL --retry 3 -o $(CACHE_DIR)/$(NODE_TARBALL) $(NODE_URL)
	@tar -xJf $(CACHE_DIR)/$(NODE_TARBALL) -C $(CACHE_DIR)
	@test -x $(NODE_BIN)
	@$(NODE_BIN) -v

node: $(NODE_BIN)

# 2. pnpm deps (only re-runs if lockfile changed)
node_modules/.pkg-stamp: package.json pnpm-lock.yaml
	@if ! command -v pnpm >/dev/null; then \
		echo "pnpm required (https://pnpm.io)" >&2; exit 1; \
	fi
	@pnpm install --frozen-lockfile
	@touch $@

deps: node_modules/.pkg-stamp

# 3. tsup bundle — PATH-prefix forces tools to resolve official node
$(BUNDLE): node_modules/.pkg-stamp tsup.sea.config.ts $(shell find src -type f 2>/dev/null) $(NODE_BIN)
	@echo "→ Bundling with $(NODE_BIN)"
	@PATH="$(dir $(NODE_BIN)):$$PATH" pnpm exec tsup --config tsup.sea.config.ts

bundle: $(BUNDLE)

# 4. SEA blob — run blob generator under the same official node
$(BLOB): $(BUNDLE) sea-config.json scripts/sea-bootstrap.cjs
	@echo "→ Generating SEA blob"
	@PATH="$(dir $(NODE_BIN)):$$PATH" $(NODE_BIN) --experimental-sea-config sea-config.json

blob: $(BLOB)

# 5. Postject injection — driven by package-linux-sea.sh so all logic
#    (cp, postject, chmod) stays in one place. NODE_BIN forces the script
#    to copy the official node binary instead of $(command -v node).
$(TARGET_BIN): $(BLOB) scripts/package-linux-sea.sh $(NODE_BIN) | check
	@NODE_BIN=$(NODE_BIN) OUT_DIR=$(OUT_DIR) BIN_NAME=$(BIN_NAME) \
		bash scripts/package-linux-sea.sh

inject: $(TARGET_BIN)

# Environment sanity
check:
	@if [ "$$(uname -s)" != "Linux" ]; then \
		echo "This Makefile must run on Linux." >&2; exit 1; \
	fi
	@if [ ! -x $(POSTJECT) ]; then \
		echo "$(POSTJECT) missing; run 'make deps' first." >&2; exit 1; \
	fi

clean:
	@rm -rf $(OUT_DIR)
	@rm -f node_modules/.pkg-stamp

distclean: clean
	@rm -rf $(CACHE_DIR)
