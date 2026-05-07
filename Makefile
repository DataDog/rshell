.PHONY: build fmt test test_all test_against_bash test_awk_rewritten test_awk_rewrite_map compliance

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

test_awk_rewritten: build
	RSHELL_BIN=./rshell AWK_UNDER_TEST=tools/awk-harness/rshell-awk tools/awk-harness/run.sh rewritten

test_awk_rewrite_map:
	tools/awk-harness/run.sh check-rewrite-map

compliance:
	RSHELL_COMPLIANCE_TEST=1 go test -v ./tests/ -run TestCompliance -count=1
