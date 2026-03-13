.PHONY: build test test_all test_against_bash compliance bench

build:
	go build -o rshell ./cmd/rshell

test:
	go test -v -race ./...

test_all:
	$(MAKE) -j2 test test_against_bash

test_against_bash:
	RSHELL_BASH_TEST=1 go test -v ./tests/ -run TestShellScenariosAgainstBash -count=1

compliance:
	RSHELL_COMPLIANCE_TEST=1 go test -v ./tests/ -run TestCompliance -count=1

bench:
	docker build -t rshell-bench -f benchmarks/Dockerfile .
	docker run --rm rshell-bench -c 'for f in /benchmarks/scripts/bench_*.sh; do bash "$$f"; done'
