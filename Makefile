.PHONY: build run test clean lint fmt tidy

BINARY_NAME=officeclaw.exe
BUILD_DIR=build

build:
	go build -ldflags="-H windowsgui" -o $(BUILD_DIR)/$(BINARY_NAME) ./src

build-console:
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./src

run:
	go run ./src

test:
	go test ./test/... -v -count=1

test-coverage:
	go test ./test/... -v -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

lint:
	golangci-lint run ./...

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html

deps:
	go mod download
	go mod tidy
