.PHONY: build run tidy lint test clean install

BIN          := markitdown
BUILD        := ../../build
OUT          := $(BUILD)/$(BIN)
INSTALL_PATH := /usr/local/bin
VERSION      := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT       := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME   := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS      := -ldflags "-s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_TIME)"

build:
	@echo "Building markitdown..."
	@mkdir -p $(BUILD)
	@go build $(LDFLAGS) -o $(OUT) .
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
