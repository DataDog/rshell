.PHONY: build fmt test test_all test_against_bash compliance

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -X github.com/DataDog/rshell/internal/version.Version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o rshell ./cmd/rshell

fmt:
	go fmt ./...

test:
	go test -v -race ./...

test_all:
	$(MAKE) -j2 test test_against_bash

test_against_bash:
	RSHELL_BASH_TEST=1 go test -v ./tests/ -run TestShellScenariosAgainstBash -count=1

compliance:
	RSHELL_COMPLIANCE_TEST=1 go test -v ./tests/ -run TestCompliance -count=1
