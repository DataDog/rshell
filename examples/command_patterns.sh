#!/usr/bin/env bash
# Curated demonstrations of the AllowedCommandPatterns feature.
#
# Run from the repository root:
#
#   bash examples/command_patterns.sh
#
# Each section runs the rshell CLI with a specific allowlist + pattern
# configuration and prints the exit code so you can see allow/deny in
# action. Edit the SCRIPTS or PATTERNS to play with your own cases.
#
# Note: this script uses `go run` so you don't need to build a binary
# first. All commands are POC-level; for repeatable benchmarking, build
# rshell once with `go build -o rshell ./cmd/rshell` and replace the
# `go run ./cmd/rshell` invocations below with `./rshell`.

set -u

# Colors for readability when run in a terminal.
if [[ -t 1 ]]; then
  BOLD=$'\033[1m'; DIM=$'\033[2m'; GREEN=$'\033[32m'; RED=$'\033[31m'; RESET=$'\033[0m'
else
  BOLD=""; DIM=""; GREEN=""; RED=""; RESET=""
fi

RSHELL=(go run ./cmd/rshell)

run_case() {
  local title="$1"; shift
  local script="$1"; shift
  printf "${BOLD}%s${RESET}\n" "$title"
  printf "${DIM}  rshell %s${RESET}\n" "$*"
  printf "${DIM}  script: %s${RESET}\n" "$script"
  set +e
  "${RSHELL[@]}" "$@" -c "$script"
  local code=$?
  set -e
  if [[ $code -eq 0 ]]; then
    printf "  ${GREEN}exit=%d (allowed)${RESET}\n\n" "$code"
  else
    printf "  ${RED}exit=%d (blocked)${RESET}\n\n" "$code"
  fi
}

cd "$(dirname "$0")/.."

# Each case below is wrapped in a comment block explaining what it
# demonstrates. Read top-to-bottom for the narrative, or jump to the
# section that's interesting.

# -----------------------------------------------------------------------
# 1. Basic prefix matching: argv begins with the pattern tokens → allowed.
# -----------------------------------------------------------------------
run_case "1. Pattern matches argv (allowed)" \
  "echo hello there" \
  --allowed-command-patterns "echo hello" -p /tmp

# -----------------------------------------------------------------------
# 2. Argv starts with a different second token → blocked. The pattern
#    "echo hello" does not admit "echo goodbye".
# -----------------------------------------------------------------------
run_case "2. Pattern does not match argv (blocked)" \
  "echo goodbye" \
  --allowed-command-patterns "echo hello" -p /tmp

# -----------------------------------------------------------------------
# 3. The architectural test: command substitution produces a name. The
#    backend (a static caller) sees only $(printf echo); the matcher
#    sees the resolved argv at execve time and applies the pattern.
#    Substitution does NOT bypass enforcement.
# -----------------------------------------------------------------------
run_case "3. Substitution-defeated escape (blocked)" \
  '$(printf echo) goodbye' \
  --allowed-commands rshell:printf \
  --allowed-command-patterns "echo hello" \
  -p /tmp

# -----------------------------------------------------------------------
# 4. Partner case: same substitution, but the resolved argv DOES match
#    the pattern → allowed. The matcher isn't blanket-rejecting
#    interpolation; it inspects the post-expansion argv.
# -----------------------------------------------------------------------
run_case "4. Substitution that matches argv (allowed)" \
  '$(printf echo) hello world' \
  --allowed-commands rshell:printf \
  --allowed-command-patterns "echo hello" \
  -p /tmp

# -----------------------------------------------------------------------
# 5. Union semantics: AllowedCommands and AllowedCommandPatterns are
#    independent permits. printf is allowed by name (any args). echo is
#    only authorised when its argv begins with "echo hello". Both calls
#    succeed because each finds its own permit.
# -----------------------------------------------------------------------
run_case "5. Union of name allowlist + pattern allowlist" \
  'printf "from-printf\n"; echo hello there' \
  --allowed-commands rshell:printf \
  --allowed-command-patterns "echo hello" \
  -p /tmp

# -----------------------------------------------------------------------
# 6. Pattern enforced inside a builtin that dispatches sub-commands.
#    `find -exec` calls back into the runner with the resolved argv,
#    which the pattern matcher inspects. Same security model, deeper
#    call site.
#
#    Setup: a temp dir with one file so find has something to iterate.
#    The pattern "echo hello" admits "echo hello /tmp/...". Without the
#    pattern matching at the find -exec call site, this would either be
#    over-permissive (echo allowed for any argv) or over-restrictive
#    (find blocks echo because the bare name isn't on the allowlist).
# -----------------------------------------------------------------------
EXAMPLE_DIR=$(mktemp -d)
touch "$EXAMPLE_DIR/probe.txt"
# The literal "\;" terminates the -exec clause inside rshell. In a
# bash double-quoted string we write it as "\\;" so a single backslash
# survives bash's interpretation and reaches rshell.
run_case "6. find -exec respects the same patterns" \
  "find $EXAMPLE_DIR -name 'probe.txt' -exec echo hello {} \\;" \
  --allowed-commands rshell:find \
  --allowed-command-patterns "echo hello" \
  -p "/tmp,$EXAMPLE_DIR"
rm -rf "$EXAMPLE_DIR"

# -----------------------------------------------------------------------
# 7. Multiple patterns: any pattern that prefix-matches admits the
#    invocation. Here "ls" alone admits any ls invocation, and the
#    "kubectl get" pattern admits only specific kubectl subcommands.
#    (kubectl is an external command, so it would also need an
#    ExecHandler to actually run. This case demonstrates the gate
#    decision; the call won't dispatch without an exec handler.)
# -----------------------------------------------------------------------
run_case "7. Multiple patterns, ls allowed (any args)" \
  "ls -la /tmp" \
  --allowed-command-patterns "ls,kubectl get" \
  -p /tmp

# -----------------------------------------------------------------------
# 8. Override: when a name appears in AllowedCommands, ALL its argv
#    forms are admitted regardless of patterns. To use prefix scoping,
#    keep the command OUT of the name allowlist. Here echo is in the
#    name allowlist so the pattern doesn't restrict it; "echo goodbye"
#    runs even though the pattern says "echo hello".
# -----------------------------------------------------------------------
run_case "8. Name allowlist overrides patterns (allowed despite pattern mismatch)" \
  "echo goodbye" \
  --allowed-commands rshell:echo \
  --allowed-command-patterns "echo hello" \
  -p /tmp

printf "${BOLD}Done.${RESET} Edit examples/command_patterns.sh to add your own cases.\n"
