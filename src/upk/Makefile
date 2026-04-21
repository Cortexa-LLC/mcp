.PHONY: build install uninstall test clean help

BIN_NAME    := upk
BUILD       := ../../build
OUT         := $(BUILD)/$(BIN_NAME)
INSTALL_DIR ?= /usr/local/bin
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -ldflags "-s -w -X main.Version=$(VERSION)"

build: ## Build upk binary
	@echo "Building $(BIN_NAME)..."
	@mkdir -p $(BUILD)
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(OUT) .
	@echo "✅ Built: $(OUT)"

install: build ## Install upk to $(INSTALL_DIR)
	@mkdir -p $(INSTALL_DIR)
	@install -m 755 $(OUT) $(INSTALL_DIR)/$(BIN_NAME)
	@echo "✅ Installed: $(INSTALL_DIR)/$(BIN_NAME)"
	@echo ""
	@echo "Initialize upk:"
	@echo "  upk init"
	@echo ""
	@echo "Add to MCP configuration:"
	@echo '  { "mcpServers": { "upk": { "command": "upk", "args": ["server", "--stdio"] } } }'

uninstall: ## Remove upk from $(INSTALL_DIR)
	@rm -f $(INSTALL_DIR)/$(BIN_NAME)
	@echo "✅ Uninstalled: $(INSTALL_DIR)/$(BIN_NAME)"

test: ## Run tests
	CGO_ENABLED=1 go test ./...

clean: ## Remove build artifacts
	@rm -rf $(BUILD)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
