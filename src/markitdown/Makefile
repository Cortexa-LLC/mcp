.PHONY: build run tidy lint test clean install

BIN := markitdown-mcp
INSTALL_PATH := /usr/local/bin

build:
	@echo "Building markitdown-mcp..."
	@go build -o $(BIN) .
	@echo "✅ Built: $(BIN)"

run: build
	./$(BIN)

tidy:
	go mod tidy

lint:
	go vet ./...

test:
	go test ./...

install: build
	@echo "Installing $(BIN) to $(INSTALL_PATH)..."
	@install -d $(INSTALL_PATH)
	@install -m 755 $(BIN) $(INSTALL_PATH)/$(BIN)
	@echo "✅ Installed: $(INSTALL_PATH)/$(BIN)"

clean:
	rm -f $(BIN)
