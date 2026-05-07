.PHONY: build test integration lint staticcheck e2e ci clean install uninstall

# Install location. Override with `make install PREFIX=$HOME/.local` for a
# user-local install, or with DESTDIR=/tmp/stage for staged packaging.
PREFIX  ?= /usr/local
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
	install -m 0755 bin/mindgate $(BINDIR)/mindgate
	@echo "installed: $(BINDIR)/mindgate"

uninstall:
	rm -f $(BINDIR)/mindgate
	@echo "removed:   $(BINDIR)/mindgate"
