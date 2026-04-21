.PHONY: all build install uninstall test clean help

# MCP servers to build/install (kglib is a library, not installed)
MCPS := kg upk markitdown slack

all: build ## Build all MCP servers

build: ## Build all MCP servers
	@echo "Building all MCP servers..."
	@for mcp in $(MCPS); do \
		echo ""; \
		echo "==> Building $$mcp..."; \
		cd src/$$mcp && $(MAKE) build || exit 1; \
		cd ../..; \
	done
	@echo ""
	@echo "✅ All MCP servers built successfully"

install: ## Install all MCP servers to /usr/local/bin
	@echo "Installing all MCP servers..."
	@for mcp in $(MCPS); do \
		echo ""; \
		echo "==> Installing $$mcp..."; \
		cd src/$$mcp && $(MAKE) install || exit 1; \
		cd ../..; \
	done
	@echo ""
	@echo "✅ All MCP servers installed successfully"
	@echo ""
	@echo "Installed binaries:"
	@ls -lh /usr/local/bin/kg /usr/local/bin/upk /usr/local/bin/markitdown /usr/local/bin/slack-mcp 2>/dev/null || true

uninstall: ## Uninstall all MCP servers from /usr/local/bin
	@echo "Uninstalling all MCP servers..."
	@for mcp in $(MCPS); do \
		echo ""; \
		echo "==> Uninstalling $$mcp..."; \
		cd src/$$mcp && $(MAKE) uninstall || exit 1; \
		cd ../..; \
	done
	@echo ""
	@echo "✅ All MCP servers uninstalled"

test: ## Run tests for all packages
	@echo "Running tests..."
	@echo ""
	@echo "==> Testing kglib..."
	@cd src/kglib && go test -v || exit 1
	@echo ""
	@for mcp in $(MCPS); do \
		echo "==> Testing $$mcp..."; \
		cd src/$$mcp && $(MAKE) test || exit 1; \
		cd ../..; \
		echo ""; \
	done
	@echo "✅ All tests passed"

clean: ## Remove all build artifacts
	@echo "Cleaning all build artifacts..."
	@for mcp in $(MCPS); do \
		cd src/$$mcp && $(MAKE) clean 2>/dev/null || true; \
		cd ../..; \
	done
	@rm -rf build
	@echo "✅ Clean complete"

help: ## Show this help
	@echo "MCP Servers - Build and Installation"
	@echo ""
	@echo "Available servers:"
	@echo "  - kg          Project knowledge graph"
	@echo "  - upk         Unified Personal Knowledge"
	@echo "  - markitdown  Document to Markdown converter"
	@echo "  - slack       Slack integration (channels, threads, messages)"
	@echo ""
	@echo "Usage:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
