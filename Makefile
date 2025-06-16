BINARY_NAME=ecsy
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-X main.version=${VERSION} -s -w"

.PHONY: all
all: build

.PHONY: build
build:
	go build ${LDFLAGS} -o ${BINARY_NAME} .

.PHONY: install
install: build
	cp ${BINARY_NAME} /usr/local/bin/

.PHONY: clean
clean:
	rm -f ${BINARY_NAME}
	rm -rf dist/

.PHONY: check-go
check-go:
	@which go > /dev/null || (echo "Error: Go is not installed. Please install Go from https://golang.org/dl/" && exit 1)

.PHONY: deps
deps: check-go
	go mod download
	go mod tidy

.PHONY: build-all
build-all: clean
	mkdir -p dist
	GOOS=darwin GOARCH=amd64 go build ${LDFLAGS} -o dist/${BINARY_NAME}-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 go build ${LDFLAGS} -o dist/${BINARY_NAME}-darwin-arm64 .
	GOOS=linux GOARCH=amd64 go build ${LDFLAGS} -o dist/${BINARY_NAME}-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build ${LDFLAGS} -o dist/${BINARY_NAME}-linux-arm64 .
	GOOS=windows GOARCH=amd64 go build ${LDFLAGS} -o dist/${BINARY_NAME}-windows-amd64.exe .

.PHONY: compress
compress: build-all
	cd dist && gzip -9 ${BINARY_NAME}-*

.PHONY: release
release: compress
	@echo "Release artifacts created in dist/"
	@ls -la dist/