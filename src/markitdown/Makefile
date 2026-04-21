.PHONY: build run tidy lint test clean install

BIN     := markitdown
BUILD   := ../../build
OUT     := $(BUILD)/$(BIN)
INSTALL_PATH := /usr/local/bin

build:
	@echo "Building markitdown..."
	@mkdir -p $(BUILD)
	@go build -o $(OUT) .
	@echo "✅ Built: $(OUT)"

run: build
	$(OUT)

tidy:
	go mod tidy

lint:
	go vet ./...

test:
	go test ./...

install: build
	@echo "Installing $(BIN) to $(INSTALL_PATH)..."
	@install -d $(INSTALL_PATH)
	@install -m 755 $(OUT) $(INSTALL_PATH)/$(BIN)
	@echo "✅ Installed: $(INSTALL_PATH)/$(BIN)"

clean:
	rm -rf $(BUILD)
