# CommandCode CPA plugin build targets.
#
# The tests are pure Go (no cgo), so they can run anywhere:
#   make test
# The shared library needs cgo and must match the CPA process architecture.

VERSION ?= 0.1.0
LDFLAGS := -X commandcode-cliproxy-plugin/internal/commandcode.Version=$(VERSION)

.PHONY: test vet fmt build-windows build-linux build-linux-arm64 build-darwin clean

test:
	go test ./internal/...

vet:
	go vet ./...

fmt:
	gofmt -w $(shell find . -name '*.go' -not -path './dist/*')

build-windows:
	mkdir -p dist
	go build -buildmode=c-shared -ldflags "$(LDFLAGS)" -o dist/commandcode.dll .

build-linux:
	mkdir -p dist
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildmode=c-shared -ldflags "$(LDFLAGS)" -o dist/commandcode.so .

build-linux-arm64:
	mkdir -p dist
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=aarch64-linux-gnu-gcc go build -buildmode=c-shared -ldflags "$(LDFLAGS)" -o dist/commandcode.so .

build-darwin:
	mkdir -p dist
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -buildmode=c-shared -ldflags "$(LDFLAGS)" -o dist/commandcode.dylib .

# Release artifacts: one zip per platform plus a checksum, matching the store
# registry entry format (store/registry-entry.json).
release: build-windows build-linux build-linux-arm64 build-darwin
	cd dist && sha256sum commandcode.* > checksums.txt && cat checksums.txt

clean:
	rm -rf dist
