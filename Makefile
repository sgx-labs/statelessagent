BINARY_NAME := same
BUILD_DIR := build
VERSION := 0.9.0
LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION)"

# CGO is required for sqlite3 + sqlite-vec
export CGO_ENABLED := 1

# Extra include path for cross-compilation (sqlite3.h)
# Also disable zig's ubsan which causes linker errors on cross-compile
CROSS_CFLAGS := -I$(CURDIR)/cgo-headers -fno-sanitize=undefined

.PHONY: all build clean test precheck provider-smoke provider-smoke-full darwin-arm64 darwin-amd64 linux-amd64 linux-arm64 windows-amd64 cross-all install

all: build

build:
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/same

test:
	go test -race ./... -v -count=1

precheck:
	@/usr/bin/env bash .scripts/precheck.sh

provider-smoke: build
	@/usr/bin/env bash .scripts/provider-smoke.sh

provider-smoke-full: build
	@SAME_SMOKE_PROVIDERS=$${SAME_SMOKE_PROVIDERS:-none,ollama,openai-compatible} \
	SAME_SMOKE_REQUIRED=$${SAME_SMOKE_REQUIRED:-none} \
	/usr/bin/env bash .scripts/provider-smoke.sh

# Native macOS arm64 build (native CC, no zig needed)
darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/same

# macOS amd64 — requires native x86_64 toolchain or Rosetta
# On arm64 Mac, use: arch -x86_64 make darwin-amd64
darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/same

# Linux amd64 (cross-compile with zig cc from macOS, or native on Linux)
linux-amd64:
	GOOS=linux GOARCH=amd64 \
	CGO_CFLAGS="$(CROSS_CFLAGS)" \
	CC="zig cc -target x86_64-linux-gnu" \
	CXX="zig c++ -target x86_64-linux-gnu" \
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/same

# Linux arm64 (cross-compile with zig cc from macOS, or native on ARM Linux)
linux-arm64:
	GOOS=linux GOARCH=arm64 \
	CGO_CFLAGS="$(CROSS_CFLAGS)" \
	CC="zig cc -target aarch64-linux-gnu" \
	CXX="zig c++ -target aarch64-linux-gnu" \
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/same

# Windows amd64 (cross-compile with zig cc)
windows-amd64:
	GOOS=windows GOARCH=amd64 \
	CGO_CFLAGS="$(CROSS_CFLAGS)" \
	CC="zig cc -target x86_64-windows-gnu" \
	CXX="zig c++ -target x86_64-windows-gnu" \
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/same

# Build all platform targets
cross-all: darwin-arm64 windows-amd64 linux-amd64 linux-arm64

# Install to ~/.local/bin (preferred), $GOPATH/bin, or /usr/local/bin.
# IMPORTANT: rm before cp to avoid macOS code signing cache issues —
# stale signatures on in-place replacement cause SIGKILL on Apple Silicon.
install: build
	@INSTALL_DIR=""; \
	if [ -d "$(HOME)/.local/bin" ]; then \
		INSTALL_DIR="$(HOME)/.local/bin"; \
	elif [ -n "$(GOPATH)" ]; then \
		INSTALL_DIR="$(GOPATH)/bin"; \
	else \
		INSTALL_DIR="/usr/local/bin"; \
	fi; \
	rm -f "$$INSTALL_DIR/$(BINARY_NAME)"; \
	cp $(BUILD_DIR)/$(BINARY_NAME) "$$INSTALL_DIR/$(BINARY_NAME)"; \
	echo "Installed to $$INSTALL_DIR/$(BINARY_NAME)"

security-test:
	go test ./internal/hooks/... ./internal/mcp/... ./internal/web/... ./internal/guard/... ./internal/store/... -run "Security|Injection|Sanitize|Plugin|RateLimit|Private|Traversal" -count=1 -v

clean:
	rm -rf $(BUILD_DIR)
