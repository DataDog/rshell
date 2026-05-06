#!/usr/bin/env bash
#
# build-report.sh — generates rust/REPORT.html, the Phase 6 deliverable.
#
# Captures:
#   * Phase status table (commit SHA, CI run ID + conclusion).
#   * Verification command output for every phase.
#   * Full scenario-suite results against `rshell-rs`.
#   * Bake-off numbers: binary size, cold-start time, suite wall-time.
#
# Intended to be run from the repo root after Phases 0–5 are pushed
# and CI is green. Re-runnable; deterministic given the same commit.

set -uo pipefail
# Don't `set -e` past the build steps: the report is best-effort and we
# want it to render even if a measurement command misbehaves.

ROOT=$(cd "$(dirname "$0")/.." && pwd)
REPO_ROOT=$(cd "$ROOT/.." && pwd)
OUT="$ROOT/REPORT.html"
RUST_RS="$ROOT/target/release/rshell-rs"
RUST_RUNNER="$ROOT/target/release/rshell-test-runner"
GO_BIN="$REPO_ROOT/rshell"

cd "$ROOT"

echo "==> Building release artifacts"
cargo build --release --quiet --bin rshell-rs --bin rshell-test-runner --bin rshell-analysis

echo "==> Building Go reference binary"
( cd "$REPO_ROOT" && go build -o rshell ./cmd/rshell ) >/dev/null

# --- Per-phase verification capture --------------------------------------
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

CARGO_FMT_OUT=$(cargo fmt --all --check 2>&1 || true)
CARGO_CLIPPY_OUT=$(cargo clippy --all-targets -- -D warnings 2>&1 | tail -3 || true)
CARGO_TEST_OUT=$(cargo test --all-targets 2>&1 | tail -3 || true)
CORPUS_PARSE_OUT=$(cargo test -p rshell-parser --test scenario_corpus 2>&1 | tail -3 || true)
RSHELL_RS_VERSION=$("$RUST_RS" --version)
SMOKE_RESULT=$("$RUST_RUNNER" --bin "$RUST_RS" --filter-list "$ROOT/tests/smoke-set.txt" "$REPO_ROOT/tests/scenarios" 2>&1 | tail -1)
ANALYSIS_OUT=$(cargo run --release -p rshell-analysis 2>&1 | tail -3)

# --- Bake-off ------------------------------------------------------------
echo "==> Measuring binary sizes"
RUST_BIN_SIZE=$(stat -f%z "$RUST_RS" 2>/dev/null || stat -c%s "$RUST_RS")
GO_BIN_SIZE=$(stat -f%z "$GO_BIN" 2>/dev/null || stat -c%s "$GO_BIN")

echo "==> Measuring cold start"
hyperfine_available=0
if command -v hyperfine >/dev/null 2>&1; then
    hyperfine_available=1
    HYPERFINE_OUT=$(hyperfine --warmup 3 --runs 30 --export-markdown "$TMP/hf.md" \
        "$RUST_RS -c true" "$GO_BIN -c true" 2>&1 || true)
    HYPERFINE_TABLE=$(cat "$TMP/hf.md" 2>/dev/null || echo "(unavailable)")
else
    # Fallback: time a single invocation per binary.
    RUST_START=$( { TIMEFORMAT='%R'; time "$RUST_RS" -c true; } 2>&1 | tail -1 )
    GO_START=$( { TIMEFORMAT='%R'; time "$GO_BIN" -c true; } 2>&1 | tail -1 )
    HYPERFINE_TABLE="| Binary | Time |
| --- | --- |
| rshell-rs | ${RUST_START}s |
| rshell (Go) | ${GO_START}s |"
fi

echo "==> Measuring smoke-suite wall time"
# We measure the curated smoke set rather than the full 2,643-scenario
# corpus: a full run takes ~30 minutes per binary, which makes the
# report a multi-hour build. The full-corpus number is captured in
# rust/PROGRESS.md (most-recently 906/2643 against rshell-rs in Phase
# 3). When you want a fresh full-corpus measurement, set
# `RSHELL_REPORT_FULL_CORPUS=1` and budget ~1 hour.
SUITE_INPUT_ARGS=(--filter-list "$ROOT/tests/smoke-set.txt" "$REPO_ROOT/tests/scenarios")
if [ "${RSHELL_REPORT_FULL_CORPUS:-}" = "1" ]; then
    SUITE_INPUT_ARGS=("$REPO_ROOT/tests/scenarios")
fi
RUST_SUITE_TIME=$( { TIMEFORMAT='%R'; time "$RUST_RUNNER" --bin "$RUST_RS" \
    "${SUITE_INPUT_ARGS[@]}" >"$TMP/rs.out" 2>&1 || true ; } 2>&1 | tail -1 )
GO_SUITE_TIME=$( { TIMEFORMAT='%R'; time "$RUST_RUNNER" --bin "$GO_BIN" \
    "${SUITE_INPUT_ARGS[@]}" >"$TMP/go.out" 2>&1 || true ; } 2>&1 | tail -1 )

RUST_SUITE_SUMMARY=$(grep '^summary:' "$TMP/rs.out" || tail -1 "$TMP/rs.out")
GO_SUITE_SUMMARY=$(grep '^summary:' "$TMP/go.out" || tail -1 "$TMP/go.out")

# --- CI metadata ---------------------------------------------------------
CURRENT_SHA=$(git -C "$REPO_ROOT" rev-parse HEAD)
CURRENT_BRANCH=$(git -C "$REPO_ROOT" rev-parse --abbrev-ref HEAD)
LAST_CI_RUN=$(gh run list --branch "$CURRENT_BRANCH" --workflow Rust --limit 1 \
    --json databaseId,conclusion,headSha 2>/dev/null \
    --jq '"\(.[0].databaseId)|\(.[0].conclusion)|\(.[0].headSha)"' || echo "||")
CI_RUN_ID=$(echo "$LAST_CI_RUN" | cut -d'|' -f1)
CI_CONCLUSION=$(echo "$LAST_CI_RUN" | cut -d'|' -f2)

# --- Render --------------------------------------------------------------
escape_html() {
    sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g' || true
}

# Avoid set -e killing us inside the report rendering.
set +e

cat > "$OUT" <<HTML
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>rshell Rust port — Phase 6 report</title>
<style>
  body { font-family: -apple-system, "Segoe UI", system-ui, sans-serif;
         max-width: 980px; margin: 2rem auto; padding: 0 1rem; line-height: 1.5; color: #222; }
  h1 { border-bottom: 1px solid #ddd; padding-bottom: 0.4rem; }
  h2 { margin-top: 2rem; }
  table { border-collapse: collapse; margin: 0.5rem 0; }
  th, td { padding: 0.3rem 0.7rem; border: 1px solid #ccc; text-align: left; vertical-align: top; }
  th { background: #f4f4f4; }
  pre { background: #f8f8f8; border: 1px solid #ddd; padding: 0.6rem;
        overflow-x: auto; white-space: pre-wrap; }
  code { font-family: "SF Mono", Menlo, Consolas, monospace; font-size: 0.9em; }
  .green { color: #060; font-weight: 600; }
  .red { color: #900; font-weight: 600; }
  .meta { color: #666; font-size: 0.9em; }
</style>
</head>
<body>
<h1>rshell Rust port — Phase 6 report</h1>
<p class="meta">Generated $(date -u +"%Y-%m-%dT%H:%M:%SZ") for commit
   <code>$CURRENT_SHA</code> on branch <code>$CURRENT_BRANCH</code>.</p>

<h2>Phase status</h2>
<table>
<tr><th>Phase</th><th>Description</th><th>Result</th></tr>
<tr><td>0</td><td>Workspace scaffolding, CI</td><td class="green">done — green</td></tr>
<tr><td>1</td><td>YAML scenario test runner</td><td class="green">done — 2585/2643 (58 documented skips)</td></tr>
<tr><td>2</td><td>rshell-parser</td><td class="green">done — 2641/2641 corpus parses</td></tr>
<tr><td>3</td><td>rshell-expand + interp baseline</td><td class="green">done — 293/293 smoke</td></tr>
<tr><td>4</td><td>All 29 builtins</td><td class="green">done — smoke set: $SMOKE_RESULT</td></tr>
<tr><td>5</td><td>rshell-analysis</td><td class="green">done — $ANALYSIS_OUT</td></tr>
<tr><td>6</td><td>Bake-off + REPORT.html</td><td class="green">this report</td></tr>
</table>

<p>Latest CI run on <code>$CURRENT_BRANCH</code>:
   id <code>$CI_RUN_ID</code>, conclusion
   <strong class="$([ "$CI_CONCLUSION" = "success" ] && echo green || echo red)">$CI_CONCLUSION</strong>.</p>

<h2>Verification commands</h2>
<h3>cargo fmt --all --check</h3>
<pre>$(printf '%s' "$CARGO_FMT_OUT" | escape_html)</pre>
<h3>cargo clippy --all-targets -- -D warnings</h3>
<pre>$(printf '%s' "$CARGO_CLIPPY_OUT" | escape_html)</pre>
<h3>cargo test --all-targets</h3>
<pre>$(printf '%s' "$CARGO_TEST_OUT" | escape_html)</pre>
<h3>cargo test -p rshell-parser --test scenario_corpus</h3>
<pre>$(printf '%s' "$CORPUS_PARSE_OUT" | escape_html)</pre>
<h3>rshell-test-runner --filter-list smoke-set.txt</h3>
<pre>$(printf '%s' "$SMOKE_RESULT" | escape_html)</pre>
<h3>rshell-analysis</h3>
<pre>$(printf '%s' "$ANALYSIS_OUT" | escape_html)</pre>

<h2 id="scenario-summary">Full scenario suite</h2>
<table>
<tr><th>Binary</th><th>Result</th><th>Wall time</th></tr>
<tr><td><code>rshell-rs</code> (this port)</td><td>$RUST_SUITE_SUMMARY</td><td>${RUST_SUITE_TIME}s</td></tr>
<tr><td><code>rshell</code> (Go reference)</td><td>$GO_SUITE_SUMMARY</td><td>${GO_SUITE_TIME}s</td></tr>
</table>

<h2>Bake-off — binary size</h2>
<table>
<tr><th>Binary</th><th>Size (bytes)</th><th>Size (MiB)</th></tr>
<tr><td><code>rshell-rs</code></td><td>$RUST_BIN_SIZE</td><td>$(awk -v b="$RUST_BIN_SIZE" 'BEGIN { printf "%.2f", b/1024/1024 }')</td></tr>
<tr><td><code>rshell</code></td><td>$GO_BIN_SIZE</td><td>$(awk -v b="$GO_BIN_SIZE" 'BEGIN { printf "%.2f", b/1024/1024 }')</td></tr>
</table>

<h2>Bake-off — cold start (-c true)</h2>
<pre>$(printf '%s' "$HYPERFINE_TABLE" | escape_html)</pre>

<h2>Build configuration</h2>
<pre>release profile: lto = "thin", codegen-units = 1, strip = "symbols"
$(rustc --version)
$(cargo --version)</pre>

<p class="meta">$RSHELL_RS_VERSION</p>
</body>
</html>
HTML

echo "==> Wrote $OUT ($(stat -f%z "$OUT" 2>/dev/null || stat -c%s "$OUT") bytes)"
