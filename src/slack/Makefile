.PHONY: build install uninstall test clean help

BIN_NAME    := slack-mcp
BUILD       := ../../build
OUT         := $(BUILD)/$(BIN_NAME)
INSTALL_DIR ?= /usr/local/bin
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME  := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS     := -ldflags "-s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_TIME)"

build: ## Build slack binary
	@echo "Building $(BIN_NAME)..."
	@mkdir -p $(BUILD)
	@go build $(LDFLAGS) -o $(OUT) .
	@echo "✅ Built: $(OUT)"

install: build ## Install slack to $(INSTALL_DIR)
	@mkdir -p $(INSTALL_DIR)
	@install -m 755 $(OUT) $(INSTALL_DIR)/$(BIN_NAME)
	@echo "✅ Installed: $(INSTALL_DIR)/$(BIN_NAME)"
	@echo ""
	@echo "1. Create config file at ~/.slack-mcp/config.json:"
	@echo '{'
	@echo '  "default_workspace": "my-workspace",'
	@echo '  "workspaces": {'
	@echo '    "my-workspace": {'
	@echo '      "access_token": "xoxe.xoxp-...",'
	@echo '      "refresh_token": "xoxe-...",'
	@echo '      "client_id": "optional",'
	@echo '      "client_secret": "optional"'
	@echo '    }'
	@echo '  }'
	@echo '}'
	@echo ""
	@echo "2. Add to MCP configuration (~/.claude.json):"
	@echo '  "slack": {'
	@echo '    "command": "slack-mcp",'
	@echo '    "args": ["server"]'
	@echo '  }'
	@echo ""
	@echo "Or specify custom config path:"
	@echo '  "slack": {'
	@echo '    "command": "slack-mcp",'
	@echo '    "args": ["server", "--config", "/path/to/config.json"]'
	@echo '  }'

uninstall: ## Remove slack from $(INSTALL_DIR)
	@rm -f $(INSTALL_DIR)/$(BIN_NAME)
	@echo "✅ Uninstalled: $(INSTALL_DIR)/$(BIN_NAME)"

test: ## Run tests
	@go test ./...

clean: ## Remove build artifacts
	@rm -rf $(BUILD)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
