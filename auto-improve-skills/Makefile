.PHONY: train

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

loop:
	for i in $(seq 0 20); do make train3; done
