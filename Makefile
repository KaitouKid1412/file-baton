VERSION ?= $(shell sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' .claude-plugin/plugin.json | head -1)
LDFLAGS := -s -w -X main.version=$(VERSION)
TARGETS := darwin-arm64 darwin-amd64 linux-arm64 linux-amd64
HOST := $(shell go env GOOS)-$(shell go env GOARCH)

.PHONY: build dist test validate e2e clean

# Host binary, used when developing with: claude --plugin-dir .
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o libexec/$(HOST)/file-baton ./cmd/file-baton

# Every release target.
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

# Two real Claude sessions racing for one file; costs a few cents.
e2e: build
	./scripts/e2e.sh

clean:
	rm -rf $(addprefix libexec/,$(TARGETS)) dist
