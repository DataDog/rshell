.PHONY: fmt test train reset-skill

fmt:
	go fmt ./...

test:
	go test ./...

reset-skill:
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
	@if git diff --quiet HEAD -- skills/remote-host-diagnostics/SKILL.md; then \
		echo "No skill changes to commit"; \
	else \
		git commit --only -m "Reset skill" -- skills/remote-host-diagnostics/SKILL.md; \
		git push; \
	fi

train:
	go run ./cmd/skilltrain \
		-model gpt-5.5 \
		-iters 100 \
		-judge
