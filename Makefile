VERSION ?= $(shell sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' .claude-plugin/plugin.json | head -1)
LDFLAGS := -s -w -X main.version=$(VERSION)
TARGETS := darwin-arm64 darwin-amd64 linux-arm64 linux-amd64

.PHONY: build dist release test validate e2e clean

# Development binary for this machine. The launcher prefers it over the
# committed release binaries; it is gitignored.
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o libexec/dev/file-baton ./cmd/file-baton

# Release binaries for every target, committed with each release.
dist:
	@for t in $(TARGETS); do \
		os=$${t%-*}; arch=$${t#*-}; \
		echo "building $$t"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o libexec/$$t/file-baton ./cmd/file-baton || exit 1; \
	done

test:
	go vet ./...
	go test -race ./...

validate:
	claude plugin validate --strict .

# Everything a release needs; then commit libexec/ and tag.
release: test validate dist
	@echo
	@echo "Release $(VERSION) built. Next:"
	@echo "  git add -A libexec .claude-plugin && git commit -m 'Release v$(VERSION)'"
	@echo "  claude plugin tag --push"

# Two real Claude sessions racing for one file; costs a few cents.
e2e: build
	./scripts/e2e.sh

clean:
	rm -rf libexec/dev dist
