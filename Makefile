.PHONY: build test integration lint e2e ci clean

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

ci: lint test integration

clean:
	rm -rf bin/ coverage.out
