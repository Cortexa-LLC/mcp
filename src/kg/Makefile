.PHONY: build install uninstall test clean help

UNAME_S     := $(shell uname -s 2>/dev/null || echo Windows)
IS_WINDOWS  := $(filter Windows_NT,$(OS))
BIN_NAME    := $(if $(IS_WINDOWS),kg-mcp.exe,kg-mcp)

# Default install dir per platform (override with: make install INSTALL_DIR=/your/path)
ifeq ($(IS_WINDOWS),Windows_NT)
  INSTALL_DIR ?= $(USERPROFILE)/AppData/Local/Programs/kg-mcp
else
  INSTALL_DIR ?= /usr/local/bin
endif
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME  := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS     := -ldflags "-s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_TIME)"

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Build the kg-mcp binary
	@echo "Building $(BIN_NAME)..."
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(BIN_NAME) .
	@echo "✅ Built: $(BIN_NAME)"

install: build ## Build and install kg-mcp to $(INSTALL_DIR)
	@mkdir -p $(INSTALL_DIR)
	@install -m 755 $(BIN_NAME) $(INSTALL_DIR)/$(BIN_NAME)
	@echo "✅ Installed: $(INSTALL_DIR)/$(BIN_NAME)"
	@echo ""
	@echo "Add to your MCP configuration:"
	@echo '  { "mcpServers": { "kg": { "command": "$(INSTALL_DIR)/$(BIN_NAME)", "args": ["handle-server", "--stdio"] } } }'

uninstall: ## Remove kg-mcp from $(INSTALL_DIR)
	@rm -f $(INSTALL_DIR)/$(BIN_NAME)
	@echo "✅ Removed: $(INSTALL_DIR)/$(BIN_NAME)"

test: ## Run tests
	CGO_ENABLED=1 go test ./...

clean: ## Remove build artifacts
	@rm -f $(BIN_NAME)
