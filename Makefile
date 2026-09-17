.PHONY: build test race bench vet fmt run probe import clean tidy all

BIN := oaiprism
ifeq ($(OS),Windows_NT)
BIN := oaiprism.exe
endif

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

all: vet test build

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN) ./cmd/oaiprism

# 交叉编译：部署到 Linux 服务器用的静态二进制。
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/oaiprism-linux-amd64 ./cmd/oaiprism
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/oaiprism-linux-arm64 ./cmd/oaiprism

vet:
	go vet ./...

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

test:
	go test ./...

race:
	go test -race ./...

bench:
	go test -bench=. -benchmem -run=^$$ ./internal/...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

run: build
	./bin/$(BIN) serve -config configs/config.yaml

run-debug: build
	./bin/$(BIN) serve -config configs/config.yaml -debug

probe: build
	./bin/$(BIN) probe -config configs/config.yaml

clean:
	rm -rf bin coverage.out
