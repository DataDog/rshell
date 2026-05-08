.PHONY: build fmt test test_all test_against_bash test_against_gawk test_awk_rewritten compliance

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

test_against_gawk:
	go test -v ./tests/ -run TestShellScenarioOracleMetadata -count=1
	tools/awk-harness/run.sh scenarios

test_awk_rewritten: build
	go test -v ./tests/ -run TestAwkScenarioMetadata -count=1
	RSHELL_BIN=./rshell AWK_UNDER_TEST=tools/awk-harness/rshell-awk tools/awk-harness/run.sh rewritten

compliance:
	RSHELL_COMPLIANCE_TEST=1 go test -v ./tests/ -run TestCompliance -count=1
