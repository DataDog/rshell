# Memory & Resource Usage Gaps

This document catalogues known resource-usage gaps in rshell — scenarios where a
single shell script can drive large memory consumption or degrade Go scheduler
performance in the host process. Each gap is accompanied by the test that
demonstrates it (or a note that no test exists yet).

---

## Gap 1: `grep -B N` Before-Context Buffer (~1 GiB)

**Location**: `builtins/grep/grep.go`

**Description**: `grep -B N` maintains a sliding window of the last N lines in a
`beforeBuf []contextLine` slice. Each entry is an explicit `make+copy` of the
line bytes. There is a cap on the number of lines (`MaxContextLines = 1000`) and
a cap on per-line size (`MaxLineBytes = 1 MiB`), but **no aggregate byte cap**
across the window. At maximum values:

```
MaxContextLines × MaxLineBytes = 1000 × (1 MiB − 1) ≈ 1 GiB
```

All 1000 copies are live simultaneously in `beforeBuf` at the moment the match
fires. The same limit applies to `-C N` (combined before+after context), which
sets both `before` and `after` to N.

**Contrast with `-A N`**: After-context lines are written directly to stdout with
no copies stored — see Gap 2.

**Test**: `builtins/grep/grep_test.go` → `TestGrepBeforeContextMemorySpike`

---

## Gap 2: `grep -A N` Caller-Side Buffer Accumulation (~1 GiB)

**Location**: caller integration (`run_command.go` in datadog-agent)

**Description**: rshell streams `grep -A N` output directly to its stdout
`io.Writer` with no internal copies (rshell's own allocation is ~2 MiB for the
scanner buffer). However, the datadog-agent caller always supplies a
`*bytes.Buffer` as stdout:

```go
// pkg/privateactionrunner/bundles/remoteaction/rshell/run_command.go
var stdout, stderr bytes.Buffer
interp.StdIO(nil, &stdout, &stderr)
```

Every byte that rshell writes accumulates in the caller's heap. A script
producing `MaxContextLines × MaxLineBytes ≈ 1 GiB` of after-context output
causes 1 GiB of growth in the agent process, even though rshell itself holds
almost nothing. The fix responsibility lies with the caller: bound output size
before executing the script.

**Tests**: `builtins/grep/grep_test.go`
- `TestGrepAfterContextNoBuffering` — shows rshell internal alloc is ~2 MiB when stdout is discarded
- `TestGrepAfterContextCallerBufferAccumulation` — shows the caller's `bytes.Buffer` accumulates ~1 GiB for the same input (~500× more)

---

## Gap 3: Sort Chain — N × 256 MiB Simultaneous Allocation

**Location**: `builtins/sort/sort.go`, `interp/runner_exec.go`

**Description**: `sort` must buffer its entire input in an `allLines []string`
slice before emitting a single byte of output (sorting cannot stream). Each sort
is capped individually at `MaxTotalBytes = 256 MiB`, but there is **no aggregate
cap across a pipeline**.

Because `runner_exec.go` runs the left side of every pipe concurrently with the
right side, all N sorts in a chain hold their `allLines` slices simultaneously at
peak:

```
sort input | sort | sort   →   peak ≈ 3 × 256 MiB = 768 MiB
```

Demonstrated with a 64 MiB input:

| Chain depth | Allocated |
|---|---|
| 1 sort | ~71 MiB |
| 2 sorts | ~143 MiB |
| 3 sorts | ~214 MiB |

**Test**: `interp/tests/pipeline_test.go` → `TestSortChainLargeMemoryConsumption`

---

## Gap 4: Unbounded Pipeline Goroutines — Go Scheduler Stability

**Location**: `interp/runner_exec.go:144`

**Description**: Each `syntax.Pipe` node spawns exactly one goroutine for the
left-side command. Pipelines are left-associative, so `a | b | c | d` spawns 3
goroutines. There is no goroutine limit, no semaphore, and no pipeline depth
validation anywhere in `interp/`.

Even goroutines that are completely blocked on pipe I/O impose overhead on the
host process:
- The Go scheduler must track every goroutine during work-stealing decisions.
- The GC must scan every goroutine's stack as a live root on every collection cycle.

With a 500-command pipeline (499 simultaneously-live goroutines), this overhead
degrades **all** goroutines in the host process — including unrelated
datadog-agent work running concurrently — not just the script being executed.

Demonstrated:

| Pipeline depth | Live goroutines (delta) |
|---|---|
| 5 commands | +5 |
| 20 commands | +20 |
| 100 commands | +100 |

**Test**: `interp/tests/pipeline_test.go` → `TestPipelineGoroutineScalesWithDepth`

---

## Gap 5: Unbounded Script Input Size — No Limit at Parse Time

**Location**: parser entry point (caller-side); no enforcement in rshell

**Description**: rshell's `Runner.Run(ctx, node)` accepts a pre-parsed
`*syntax.File` AST. Parsing is performed by the caller using
`mvdan.cc/sh/v3/syntax.NewParser().Parse(reader)`. Neither rshell nor the parser
enforce any size limit on the script source.

The mvdan parser reads the source in a **1 KiB rolling buffer** (it does not
slurp the full source into memory). However:

- The source string already occupies `len(script)` bytes in the caller's heap
  before parsing begins (in dd-agent, it arrives as a JSON string field).
- The AST stores string values for every `Lit` node (identifiers, arguments,
  heredoc bodies). A script with a large embedded string literal causes an
  additional allocation proportional to that literal's size — up to 2× the
  script size for the literal-heavy portion.

The datadog-agent enforces a 15 MiB limit on `inputs.Command` at the ingestion
layer. rshell itself has no equivalent limit and relies entirely on callers to
enforce one. Any caller that omits this check can feed an arbitrarily large script
to the parser with no rejection.

**Contrast with runtime limits already enforced by rshell**:

| Data | Limit |
|---|---|
| Variable value | 1 MiB (`MaxVarBytes`) |
| Command substitution output | 1 MiB (`maxCmdSubstOutput`) |
| Per-line in builtins (grep, sort, tail, …) | 1 MiB (`MaxLineBytes`) |
| sort total input | 256 MiB (`MaxTotalBytes`) |
| **Script source size** | **None** |

**Recommended fix**: Add a `MaxScriptBytes` constant and a rshell-owned
`Parse(r io.Reader) (*syntax.File, error)` helper that wraps the reader with
`io.LimitReader(r, MaxScriptBytes+1)` before handing it to the mvdan parser.
Callers would use `rshell.Parse(reader)` instead of calling the mvdan parser
directly, getting the limit for free.

**Test**: None yet.
