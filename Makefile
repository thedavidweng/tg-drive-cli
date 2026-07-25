BINARY_NAME=td
DIST_DIR=dist

.PHONY: all bootstrap build test test-race test-core-wasm clean lint fmt fmt-check vet ci-local snapshot goreleaser-check run-doctor

all: ci-local build

bootstrap:
	go mod tidy

build:
	mkdir -p $(DIST_DIR)
	go build -trimpath -o $(DIST_DIR)/$(BINARY_NAME) ./cmd/td

build-wasm:
	mkdir -p $(DIST_DIR)
	GOOS=js GOARCH=wasm go build -trimpath -o $(DIST_DIR)/td.wasm ./cmd/td-wasm

test:
	go test ./...

test-core-wasm:
	GOOS=js GOARCH=wasm go test -c ./core/...
	GOOS=js GOARCH=wasm go build ./core/...
	rm -f *.test

test-race:
	go test -race ./...

fmt:
	gofmt -s -w $$(git ls-files '*.go' 2>/dev/null || find . -name '*.go')
	@if command -v goimports >/dev/null 2>&1; then goimports -w $$(git ls-files '*.go' 2>/dev/null || find . -name '*.go'); fi

fmt-check:
	@test -z "$$(gofmt -s -l $$(git ls-files '*.go' 2>/dev/null || find . -name '*.go'))" || (echo "gofmt: files need formatting:" && gofmt -s -l $$(git ls-files '*.go' 2>/dev/null || find . -name '*.go') && exit 1)

vet:
	go vet ./...

lint: fmt-check vet
	@command -v golangci-lint >/dev/null 2>&1 || (echo "golangci-lint not installed; run: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.1.6" && exit 1)
	golangci-lint run

ci-local: fmt-check vet test test-race test-core-wasm

clean:
	rm -rf $(DIST_DIR)
	rm -f *.test

run-doctor: build
	./$(DIST_DIR)/$(BINARY_NAME) doctor

goreleaser-check:
	goreleaser check

# Local snapshot skips cosign signing: keyless signing needs CI OIDC.
snapshot:
	goreleaser release --snapshot --clean --skip=sign
