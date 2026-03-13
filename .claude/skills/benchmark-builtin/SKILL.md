---
name: benchmark-builtin
description: Create hyperfine benchmarks comparing a builtin command in rshell vs bash
argument-hint: "<command-name>"
---

Create benchmarks for the **$ARGUMENTS** builtin command.

---

## ⛔ STOP — READ THIS BEFORE DOING ANYTHING ELSE ⛔

You MUST follow this execution protocol. Skipping steps has caused defects in every prior run of this skill.

### 1. Create the full task list FIRST

Your very first action — before reading ANY files, before writing ANY code — is to call TaskCreate exactly 5 times, once for each step below (Steps 1–5). Use these exact subjects:

1. "Step 1: Research the builtin"
2. "Step 2: Ensure benchmark infrastructure exists"
3. "Step 3: Design benchmark matrix"
4. "Step 4: Create benchmark script"
5. "Step 5: Run and verify"

### 2. Execution order and gating

Steps run in this order:

```
Step 1 → Step 2 → Step 3 → Step 4 → Step 5
```

**Sequential steps:** Before starting step N, call TaskList and verify step N-1 is `completed`. Set step N to `in_progress`.

Before marking any step as `completed`:
- Re-read the step description and verify every sub-bullet is satisfied
- If any sub-bullet is not done, keep working — do NOT mark it completed

### 3. Never skip steps

- Do NOT skip research (Step 1) because you think you already know the command
- Do NOT skip infrastructure checks (Step 2) because "the Dockerfile probably exists"
- Do NOT skip verification (Step 5) because "the script looks correct"

If you catch yourself wanting to skip a step, STOP and do the step anyway.

---

## Context

The safe shell interpreter (`interp/`) implements all commands as Go builtins — it never executes host binaries. Benchmarks compare the built `rshell` binary against `bash` using [hyperfine](https://github.com/sharkdp/hyperfine) inside a Docker container (Debian bookworm-slim) for a consistent, reproducible environment.

Key structural facts:
- Benchmark scripts live in `benchmarks/scripts/bench_{name}.sh`
- Shared helpers live in `benchmarks/scripts/common.sh`
- The Docker image is built via `benchmarks/Dockerfile` (multi-stage: builds rshell, installs hyperfine)
- `make bench` builds the Docker image and runs ALL `bench_*.sh` scripts
- The rshell CLI uses `-s` for inline scripts and `-a` for allowed paths

### How benchmarks work

Each benchmark script:
1. Sources `common.sh` for the `header` and `bench` helpers
2. Creates test fixtures (files, directories) in a temp directory
3. Calls `bench "name" "rshell command" "bash command"` for each comparison
4. hyperfine runs both commands with `--shell=none` for accurate timing

The `bench` helper runs hyperfine with configurable warmup (`WARMUP`, default 10) and runs (`RUNS`, default 50). Results can be exported to JSON via the `EXPORT` env var.

### Command invocation patterns

rshell does not support `cd`, so commands must use explicit paths:
```bash
# ✅ Correct — pass directory as argument, allow access via -a
rshell -s 'ls $dir' -a '$dir'

# ❌ Wrong — cd is not a builtin in rshell
rshell -s 'cd $dir && ls' -a '$dir'

# ❌ Wrong — ls defaults to "." which may not be in the allowed path
rshell -s 'ls' -a '$dir'
```

For commands that read files (head, cat, grep, etc.):
```bash
rshell -s 'head -n 10 $dir/input.txt' -a '$dir'
```

The bash side should use equivalent commands. Prefer explicit paths for consistency:
```bash
bash -c 'ls $dir'
bash -c 'head -n 10 $dir/input.txt'
```

## Step 1: Research the builtin

Before writing any code:

1. Read `interp/builtins/$ARGUMENTS.go` (or `interp/builtins/$ARGUMENTS/$ARGUMENTS.go` if organised in a subdirectory) to understand:
   - All code paths, flags, and modes
   - Input sources (file, stdin, multiple files)
   - I/O patterns (streaming vs buffered, line-based vs byte-based)
   - Any internal constants (buffer sizes, limits, caps)

2. Classify the builtin into one of these categories:
   - **I/O-bound**: Commands that primarily read/write data — `cat`, `head`, `tail`, `wc`, `grep`, `cut`, `sed`, `sort`, `tr`, `uniq`, `strings`
   - **Compute-bound**: Commands that do significant processing — `grep` with regex, `sort`, `sed` with complex patterns
   - **Filesystem-bound**: Commands that read directory/file metadata — `ls`
   - **Trivial**: Commands with minimal work — `echo`, `printf`, `true`, `false`, `exit`, `break`, `continue`, `test`

3. Read existing benchmark scripts in `benchmarks/scripts/` for pattern reference.

## Step 2: Ensure benchmark infrastructure exists

Check that the following files exist and are correct:

### `benchmarks/Dockerfile`

Must be a multi-stage build that:
- Stage 1 (`builder`): Uses `golang:<version>-bookworm`, copies source, builds rshell binary
- Stage 2: Uses `debian:bookworm-slim`, installs `hyperfine`, copies the built binary and scripts

### `benchmarks/scripts/common.sh`

Must provide:
- `header "title"` — prints a section header
- `bench "name" "rshell_cmd" "bash_cmd"` — runs a hyperfine comparison with `--shell=none`
- Configurable via `WARMUP` (default 3), `RUNS` (default 10), `EXPORT` (optional JSON output dir)

### `Makefile`

Must have a `bench` target that builds the Docker image and runs all benchmark scripts:
```makefile
bench:
	docker build -t rshell-bench -f benchmarks/Dockerfile .
	docker run --rm rshell-bench -c 'for f in /benchmarks/scripts/bench_*.sh; do bash "$$f"; done'
```

If any of these are missing, create them before proceeding.

## Step 3: Design benchmark matrix

Based on the builtin's classification from Step 1, design the benchmark matrix.

### For I/O-bound builtins

Benchmark axes:
- **Flag modes**: Each major flag or mode the command supports (e.g. for `head`: lines mode `-n`, bytes mode `-c`)
- **Input sizes**: Standard set — `small` (10 lines), `medium` (1K lines), `large` (100K lines)
- **Input sources** (if applicable): file, stdin, multiple files

### For filesystem-bound builtins (e.g. ls)

Benchmark axes:
- **Flag modes**: default, `-l` (long format), `-R` (recursive)
- **Directory sizes**: `small` (10 files), `medium` (100 files), `large` (1000 files)

### For compute-bound builtins

Same as I/O-bound, plus:
- **Pattern complexity**: simple literal, simple regex, complex regex

### For trivial builtins

Fewer axes — focus on argument count and basic throughput.

Document the chosen matrix before proceeding to implementation.

## Step 4: Create benchmark script

Create `benchmarks/scripts/bench_$ARGUMENTS.sh`.

### Template

```bash
#!/usr/bin/env bash
# Benchmark: $ARGUMENTS builtin — rshell vs bash
#
# Compares rshell's built-in $ARGUMENTS against system $ARGUMENTS invoked via bash.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/common.sh"

# --- Fixture setup -----------------------------------------------------------

# Add fixture setup functions here (e.g. create files, directories).
# Use printf and seq for reproducible data generation.

# --- Benchmarks ---------------------------------------------------------------

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

header "$ARGUMENTS — mode description"

for size in small:10 medium:100 large:1000; do
    label="${size%%:*}"
    count="${size##*:}"
    dir="$TMPDIR/${label}"
    # Set up fixtures for this size...
    bench "$ARGUMENTS/mode/$label" \
        "rshell -s '$ARGUMENTS <args> $dir' -a '$dir'" \
        "bash -c '$ARGUMENTS <args> $dir'"
done
```

### Rules for benchmark scripts

1. **Make it executable**: `chmod +x` the script.
2. **Source `common.sh`**: Always source it for the `header` and `bench` helpers.
3. **Clean up**: Use `trap 'rm -rf "$TMPDIR"' EXIT` to clean up temp fixtures.
4. **Explicit paths**: Pass directories/files as explicit arguments to both rshell and bash commands. Do NOT rely on `cd` or the current working directory for rshell.
5. **Allowed paths**: Always pass `-a '$dir'` to rshell so the command can access the test fixtures.
6. **Consistent fixtures**: Both rshell and bash must operate on the same test data.
7. **No WARMUP/RUNS override**: Do not set `WARMUP` or `RUNS` in the script — let `common.sh` defaults apply (overridable by the user via environment variables).

## Step 5: Run and verify

Build and run the benchmarks via Docker:

```bash
make bench
```

Verify:
- Docker image builds successfully
- All benchmarks run without errors
- hyperfine produces meaningful timing comparisons
- Both rshell and bash commands produce the expected output (spot-check by running manually in the container)
- No `--shell=none` related failures (commands must be fully specified, not shell expressions)

If any benchmark fails, debug by running interactively:
```bash
docker run --rm -it rshell-bench
# Then inside the container:
bash /benchmarks/scripts/bench_$ARGUMENTS.sh
```

Only mark this step complete when `make bench` runs cleanly end-to-end.
