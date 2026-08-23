.PHONY: build install uninstall test clean help

UNAME_S     := $(shell uname -s 2>/dev/null || echo Windows)
IS_WINDOWS  := $(filter Windows_NT,$(OS))
BIN_NAME    := $(if $(IS_WINDOWS),kg.exe,kg)
BUILD       := ../../build
OUT         := $(BUILD)/$(BIN_NAME)

# Default install dir per platform (override with: make install INSTALL_DIR=/your/path)
ifeq ($(IS_WINDOWS),Windows_NT)
  INSTALL_DIR ?= $(USERPROFILE)/AppData/Local/Programs/kg
else
  INSTALL_DIR ?= /usr/local/bin
endif
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME  := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS     := -ldflags "-s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_TIME) -extldflags '-Wl,-no_warn_duplicate_libraries'"

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Build the kg binary
	@echo "Building $(BIN_NAME)..."
	@mkdir -p $(BUILD)
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(OUT) .
	@echo "✅ Built: $(OUT)"

# install keeps the binary it replaces. Kuzu pins its storage format to the
# library version, so a database written before a go-kuzu bump may only be
# readable by the build that wrote it. Retention is delegated to install.py
# rather than reimplemented here, so the curl path and the make path cannot
# drift; it is best-effort, and never blocks the install.
install: build ## Build and install kg to $(INSTALL_DIR)
	@mkdir -p $(INSTALL_DIR)
	@python3 ../../install.py --retain-only $(INSTALL_DIR)/$(BIN_NAME) 2>/dev/null || true
	@install -m 755 $(OUT) $(INSTALL_DIR)/$(BIN_NAME)
	@echo "✅ Installed: $(INSTALL_DIR)/$(BIN_NAME)"
	@echo ""
	@echo "Add to your MCP configuration:"
	@echo '  { "mcpServers": { "kg": { "command": "$(INSTALL_DIR)/$(BIN_NAME)", "args": ["server", "--stdio"] } } }'

uninstall: ## Remove kg from $(INSTALL_DIR)
	@rm -f $(INSTALL_DIR)/$(BIN_NAME)
	@echo "✅ Removed: $(INSTALL_DIR)/$(BIN_NAME)"

test: ## Run tests
	CGO_ENABLED=1 go test ./...

clean: ## Remove build artifacts
	@rm -rf $(BUILD)
