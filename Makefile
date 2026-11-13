VERSION ?= 0.1.0
LDFLAGS := -s -w -X github.com/marcpope/cortendesk-agent/internal/version.Version=$(VERSION)
TARGETS := windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

.PHONY: build test vuln dist manifest clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o cortendesk-agent ./cmd/cortendesk-agent

test:
	go vet ./...
	go test ./...

# Known vulnerabilities in the Go version and modules this build uses.
vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

dist:
	@mkdir -p dist
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o dist/cortendesk-agent_$${os}_$${arch}$$ext ./cmd/cortendesk-agent || exit 1; \
	done
	@# One macOS binary for both CPUs (lipo ships with Xcode).
	@if command -v lipo >/dev/null; then \
		lipo -create -output dist/cortendesk-agent_darwin_universal dist/cortendesk-agent_darwin_amd64 dist/cortendesk-agent_darwin_arm64 && \
		rm dist/cortendesk-agent_darwin_amd64 dist/cortendesk-agent_darwin_arm64; \
	fi
	cd dist && shasum -a 256 cortendesk-agent_* > SHA256SUMS

# Prints the manifest block for the console. Run after code signing and
# notarizing, which change the bytes.
manifest:
	go run ./tools/release-sign sign -version $(VERSION) dist

clean:
	rm -rf dist cortendesk-agent
