.PHONY: build test integration lint staticcheck e2e ci clean install uninstall

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
	$(GO) build -o bin/mindgate ./cmd/mindgate

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

ci: lint staticcheck test integration

clean:
	rm -rf bin/ coverage.out

install: build
	@mkdir -p $(BINDIR)
	@install -m 0755 bin/mindgate $(BINDIR)/mindgate
	@echo "installed: $(BINDIR)/mindgate"
	@case ":$$PATH:" in \
	  *":$(BINDIR):"*) ;; \
	  *) printf 'note: %s is not on $$PATH — add it (e.g. in ~/.bashrc) to call mindgate directly\n' '$(BINDIR)' ;; \
	esac

uninstall:
	@rm -f $(BINDIR)/mindgate
	@echo "removed:   $(BINDIR)/mindgate"
