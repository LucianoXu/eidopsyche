.PHONY: build test integration lint staticcheck e2e ci clean install uninstall snapshot

# Resolve `go` at parse time: PATH first, then common install locations.
# Override at any time with `make build GO=/path/to/go`.
GO ?= $(shell \
    if command -v go >/dev/null 2>&1; then command -v go; \
    elif [ -x "$$HOME/go/bin/go" ]; then echo "$$HOME/go/bin/go"; \
    elif [ -x /usr/local/go/bin/go ]; then echo /usr/local/go/bin/go; \
    elif [ -x /opt/go/bin/go ]; then echo /opt/go/bin/go; \
    fi)

ifeq ($(strip $(GO)),)
$(error 'go' not found in PATH or common install locations ($$HOME/go/bin, /usr/local/go/bin, /opt/go/bin). Install from https://go.dev/dl/ or set GO=/path/to/go)
endif

GOBIN := $(shell $(GO) env GOPATH)/bin
GOFMT := $(dir $(GO))gofmt

# Install location.
# Default: user-local (~/.local/bin) — no sudo needed.
# System-wide: sudo make install PREFIX=/usr/local
# Staged packaging: make install DESTDIR=/tmp/stage PREFIX=/usr/local
PREFIX  ?= $(HOME)/.local
DESTDIR ?=
BINDIR  ?= $(DESTDIR)$(PREFIX)/bin

build:
	$(GO) build -o bin/eidos ./cmd/eidos

test:
	$(GO) test ./...

integration:
	$(GO) test -tags=integration ./test/integration/...

lint:
	$(GOFMT) -l . | tee /dev/stderr | (! grep .)
	$(GO) vet ./...

e2e:
	@echo "e2e via docker-compose — wired in Task 16"

staticcheck:
	@command -v $(GOBIN)/staticcheck >/dev/null 2>&1 || $(GO) install honnef.co/go/tools/cmd/staticcheck@latest
	$(GOBIN)/staticcheck ./...

# Snapshot release: cross-compiles for all targets and produces archives in
# dist/, but does NOT publish. Validates .goreleaser.yml end-to-end.
snapshot:
	@command -v $(GOBIN)/goreleaser >/dev/null 2>&1 || $(GO) install github.com/goreleaser/goreleaser/v2@latest
	$(GOBIN)/goreleaser release --snapshot --clean --skip=publish

ci: lint staticcheck test integration

clean:
	rm -rf bin/ dist/ coverage.out

install: build
	@mkdir -p $(BINDIR)
	@install -m 0755 bin/eidos $(BINDIR)/eidos
	@echo "installed: $(BINDIR)/eidos"
	@case ":$$PATH:" in \
	  *":$(BINDIR):"*) ;; \
	  *) printf 'note: %s is not on $$PATH — add it (e.g. in ~/.bashrc) to call eidos directly\n' '$(BINDIR)' ;; \
	esac

uninstall:
	@rm -f $(BINDIR)/eidos
	@echo "removed:   $(BINDIR)/eidos"

IMAGE_TAG ?= dev

.PHONY: image
image:
	docker build -t ghcr.io/lucianoxu/eidopsyche-mindform:$(IMAGE_TAG) -f docker/mindform/Dockerfile .

.PHONY: image-push
image-push: image
	docker push ghcr.io/lucianoxu/eidopsyche-mindform:$(IMAGE_TAG)
