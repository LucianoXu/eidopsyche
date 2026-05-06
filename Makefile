.PHONY: build test integration lint staticcheck e2e ci clean

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
