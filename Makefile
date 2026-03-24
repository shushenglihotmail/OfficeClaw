.PHONY: build run test clean lint fmt tidy memory-health memory-reindex memory-context memory-search service-install service-uninstall

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

# Windows service management (run as admin)
service-install:
	$(BUILD_DIR)/$(BINARY_NAME) service install

service-uninstall:
	$(BUILD_DIR)/$(BINARY_NAME) service uninstall

# Memory service utilities (service deployed separately via LLMCrawl)
# Deploy: docker compose -f docker-compose.memory.yml up -d (from LLMCrawl repo)
memory-health:
	curl -s http://localhost:8007/health | jq .

memory-reindex:
	curl -s -X POST http://localhost:8007/reindex | jq .

memory-context:
	curl -s "http://localhost:8007/context?max_tokens=2000" | jq .

memory-search:
	@echo "Enter search query:" && read q && \
	curl -s -X POST http://localhost:8007/search \
		-H "Content-Type: application/json" \
		-d "{\"query\": \"$$q\", \"limit\": 5}" | jq .
