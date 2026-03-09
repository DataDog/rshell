.PHONY: test test_against_bash

test:
	go test -v ./...

test_against_bash:
	RSHELL_BASH_TEST=1 go test -v ./tests/ -run TestShellScenariosAgainstBash -count=1
