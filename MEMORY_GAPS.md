# Memory & Resource Usage Gaps

## Gap 1: `grep -B N` / `-C N` — before-context buffer (~1 GiB)

`grep -B 1000` keeps a sliding window of the last 1000 lines, copying each line into memory. With 1 MiB lines this peaks at ~1 GiB with no aggregate byte cap. **Test**: `TestGrepBeforeContextMemorySpike`.

## Gap 2: `grep -A N` — caller-side output accumulation (~1 GiB)

rshell streams after-context output directly to the caller's stdout writer (~2 MiB internal). The datadog-agent caller collects stdout into a `bytes.Buffer`, so ~1 GiB accumulates in the agent's heap. **Tests**: `TestGrepAfterContextNoBuffering` (rshell side, ~2 MiB) and `TestGrepAfterContextCallerBufferAccumulation` (caller side, ~1 GiB).

## Gap 3: Sort chain — N × 256 MiB simultaneous allocation

Each `sort` in a pipeline buffers its full input before emitting output. Because both sides of a pipe run concurrently, N sorts hold their buffers simultaneously — no aggregate cap across the chain. With a 64 MiB input, a 3-sort chain allocates ~214 MiB; the theoretical maximum (256 MiB input) is 768 MiB. **Test**: `TestSortChainLargeMemoryConsumption`.

## Gap 4: Deep pipelines — unbounded goroutine count

Each pipe operator spawns a goroutine for the left-side command. All N-1 goroutines in a pipeline stay alive simultaneously, burdening the Go scheduler and GC across the entire host process — even when blocked on I/O. A 100-command pipeline creates 100 live goroutines with no enforced limit. **Test**: `TestPipelineGoroutineScalesWithDepth`.

## Gap 5: Script input — no size limit at parse time

rshell accepts a pre-parsed AST with no check on how large the source script was. Unlike all other inputs (variables, command substitution, per-line builtins), the script itself has no cap inside rshell — it relies entirely on callers to enforce one. **No test yet.**
