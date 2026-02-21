.PHONY: build run tidy lint test clean

BIN := markitdown-mcp

build:
	go build -o $(BIN) .

run: build
	./$(BIN)

tidy:
	go mod tidy

lint:
	go vet ./...

test:
	go test ./...

clean:
	rm -f $(BIN)
