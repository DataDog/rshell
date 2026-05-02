.PHONY: fmt test train train3 trainloop

fmt:
	go fmt ./...

test:
	go test ./...

train:
	go run ./cmd/skilltrain \
		-model openai-codex/gpt-5.5 \
		-iters 100 \
		-judge

train3:
	go run ./cmd/skilltrain \
		-model openai-codex/gpt-5.5 \
		-iters 3 \
		-judge

trainloop:
	go run ./cmd/skilltrain \
		-model openai-codex/gpt-5.5 \
		-iters 3 \
		-judge \
		-feedback-llm \
		-loop-count 30
