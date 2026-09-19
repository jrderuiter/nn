GO ?= go

.PHONY: build test golden integration lint install

build:
	$(GO) build -o bin/nn .

test:
	$(GO) test ./...

# Rewrite the golden profiles after an intentional change to the generators.
golden:
	$(GO) test ./internal/cli -update

# Checks that need the real nono binary.
integration:
	$(GO) test -tags integration ./...

lint:
	gofmt -l . && $(GO) vet ./...

install:
	$(GO) install .
