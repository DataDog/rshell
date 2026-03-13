FROM golang:1.25.6-bookworm AS builder

COPY . /src
WORKDIR /src
RUN go build -o /rshell ./cmd/rshell

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends hyperfine \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /rshell /usr/local/bin/rshell
COPY benchmarks/scripts/ /benchmarks/scripts/

WORKDIR /benchmarks
ENTRYPOINT ["/bin/bash"]
