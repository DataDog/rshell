.PHONY: fmt test train train3 trainloop reset-remote-host-diagnostics

fmt:
	go fmt ./...

test:
	go test ./...

reset-remote-host-diagnostics:
	@printf '%s\n' \
		'---' \
		'name: datadog/remote-host-diagnostics' \
		'description: Diagnose using ./rshell' \
		'---' \
		'' \
		'# Remote Host Diagnostics' \
		'' \
		'Use `./rshell`' \
		'' \
		> skills/remote-host-diagnostics/SKILL.md

train:
	go run ./cmd/skilltrain \
		-model openai-codex/gpt-5.5 \
		-iters 3 \
		-judge \
		-feedback-llm \
		-loop-count 100
