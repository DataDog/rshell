.PHONY: build fmt test test_all test_against_bash compliance \
        rust-build rust-test rust-fmt rust-fmt-check rust-lint rust-all

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

# --- Rust port (rust/ subdir) ---

rust-build:
	cd rust && cargo build --all-targets

rust-test:
	cd rust && cargo test --all-targets

rust-fmt:
	cd rust && cargo fmt --all

rust-fmt-check:
	cd rust && cargo fmt --all --check

rust-lint:
	cd rust && cargo clippy --all-targets -- -D warnings

rust-all: rust-fmt-check rust-lint rust-build rust-test
