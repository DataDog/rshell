.PHONY: build fmt test test_all test_against_bash compliance correctness-scan

TARGET ?= changed
DEPTH ?= quick
CORRECTNESS_SCAN_ARGS ?=

build:
	go build -o rshell ./cmd/rshell

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

correctness-scan:
	go run ./tools/correctness-scan --target "$(TARGET)" --depth "$(DEPTH)" $(CORRECTNESS_SCAN_ARGS)
