.PHONY: build test integration lint staticcheck e2e ci clean install uninstall

# Install location.
# Default: user-local (~/.local/bin) — no sudo needed.
# System-wide: sudo make install PREFIX=/usr/local
# Staged packaging: make install DESTDIR=/tmp/stage PREFIX=/usr/local
PREFIX  ?= $(HOME)/.local
DESTDIR ?=
BINDIR  ?= $(DESTDIR)$(PREFIX)/bin

build:
	go build -o bin/mindgate ./cmd/mindgate

test:
	go test ./...

integration:
	go test -tags=integration ./test/integration/...

lint:
	gofmt -l . | tee /dev/stderr | (! grep .)
	go vet ./...

e2e:
	@echo "e2e via docker-compose — wired in Task 16"

staticcheck:
	@which staticcheck >/dev/null 2>&1 || go install honnef.co/go/tools/cmd/staticcheck@latest
	$(shell go env GOPATH)/bin/staticcheck ./...

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
