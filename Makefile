.PHONY: help test test-race test-coverage lint tidy build install clean

help:
	@echo "Targets:"
	@echo "  test           - run unit + integration tests"
	@echo "  test-race      - run with Go race detector"
	@echo "  test-coverage  - produce coverage.html and print total %"
	@echo "  lint           - run golangci-lint"
	@echo "  tidy           - go mod tidy"
	@echo "  build          - build the kdrive-fuse binary into ./bin"
	@echo "  install        - build and install binary to ~/bin"
	@echo "  clean          - remove build artifacts"

test:
	go test ./pkg/... ./internal/...

test-race:
	go test -race ./pkg/... ./internal/...

test-coverage:
	go test -race -coverprofile=coverage.out -covermode=atomic \
		-coverpkg=./pkg/...,./internal/... \
		./pkg/... ./internal/...
	go tool cover -html=coverage.out -o coverage.html
	@go tool cover -func=coverage.out | awk '/^total:/{print "total: "$$3}'

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

build:
	mkdir -p bin
	go build -o bin/kdrive-fuse ./cmd/kdrive-fuse

install: build
	install -m 0755 bin/kdrive-fuse $${HOME}/bin/kdrive-fuse

clean:
	rm -rf bin coverage.out coverage.html cover.out
