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
# The script builds rshell into a temp directory at start (so the exit
# code from a blocked command — 127 — propagates back to the shell;
# `go run` rewrites non-zero exits to 1, which would obscure whether a
# case was blocked by policy or failed for some other reason).
#
# ─── Mental model ─────────────────────────────────────────────────────
# A pattern is shaped like (command [, subcommand_path...]). The matcher
# accepts an argv when:
#   - argv[0] equals pattern[0] exactly (the command name), AND
#   - each remaining pattern token appears as an exact-match token
#     somewhere in argv[1..] (position and order don't matter, so flags
#     can interleave between argv[0] and the subcommand).
#
# Example: pattern (kubectl, get) admits BOTH "kubectl get pods" and
# "kubectl -n ns get pods", but not "kubectl delete pod foo".
#
# rshell's CLI doesn't ship kubectl as a builtin, so this script uses
# `ip` (which has real subcommands: route, addr, link) as the kubectl
# analog where it matters. echo is used for cases that only need a
# bare command name with arbitrary args.

set -u

# Colors for readability when run in a terminal.
if [[ -t 1 ]]; then
  BOLD=$'\033[1m'; DIM=$'\033[2m'; GREEN=$'\033[32m'; RED=$'\033[31m'; RESET=$'\033[0m'
else
  BOLD=""; DIM=""; GREEN=""; RED=""; RESET=""
fi

cd "$(dirname "$0")/.."

# Build once so exit codes propagate accurately. go run remaps non-zero
# exits to 1, which would mask the 127 we use to detect "blocked by
# policy".
BIN_DIR=$(mktemp -d)
trap 'rm -rf "$BIN_DIR"' EXIT
printf "${BOLD:-}Building rshell once into %s …${RESET:-}\n" "$BIN_DIR"
go build -o "$BIN_DIR/rshell" ./cmd/rshell
RSHELL=("$BIN_DIR/rshell")
printf "Done. Running cases:\n\n"

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
  if [[ $code -eq 127 ]]; then
    printf "  ${RED}exit=%d (BLOCKED by policy)${RESET}\n\n" "$code"
  elif [[ $code -eq 0 ]]; then
    printf "  ${GREEN}exit=%d (allowed, command succeeded)${RESET}\n\n" "$code"
  else
    printf "  ${GREEN}exit=%d (allowed by policy; command itself returned non-zero)${RESET}\n\n" "$code"
  fi
}

# -----------------------------------------------------------------------
# 1. Single-token pattern — admits any args. Use this shape for commands
#    without subcommands (echo, ls, cat). Pattern (echo) allows every
#    echo invocation.
# -----------------------------------------------------------------------
run_case "1. Single-token pattern (echo) — admits any echo invocation" \
  "echo hello world" \
  --allowed-command-patterns "echo" -p /tmp

# -----------------------------------------------------------------------
# 2. Two-token pattern — command + subcommand. Pattern (ip, route)
#    admits "ip route show". This is the canonical use case.
# -----------------------------------------------------------------------
run_case "2. Pattern (ip route) — subcommand match (allowed)" \
  "ip route show" \
  --allowed-command-patterns "ip route" -p /tmp

# -----------------------------------------------------------------------
# 3. Sibling subcommand is blocked. Pattern (ip, route) does NOT admit
#    "ip addr show" because the token "route" is absent from argv.
# -----------------------------------------------------------------------
run_case "3. Pattern (ip route) blocks sibling subcommand (ip addr)" \
  "ip addr show" \
  --allowed-command-patterns "ip route" -p /tmp

# -----------------------------------------------------------------------
# 4. The flag-interleaving case — the whole point of order-insensitive
#    matching. Pattern (ip, route) admits "ip -4 route show" because
#    argv[0]="ip" matches and the token "route" appears in argv[1..]
#    regardless of the -4 flag inserted before it. This is the kubectl
#    -n ns get scenario in disguise.
# -----------------------------------------------------------------------
run_case "4. Pattern (ip route) tolerates flags between command and subcommand" \
  "ip -4 route show" \
  --allowed-command-patterns "ip route" -p /tmp

# -----------------------------------------------------------------------
# 5. The architectural test — substitution-defeat. The literal command
#    text $(printf ip) addr is opaque to any static caller. rshell
#    expands it to ["ip","addr"] at runtime, the matcher applies
#    pattern (ip, route) against that argv, and the absence of "route"
#    causes a refusal at execve time. printf is allowed by name so the
#    substitution itself can run.
# -----------------------------------------------------------------------
run_case "5. Substitution-defeated escape (blocked)" \
  '$(printf ip) addr' \
  --allowed-commands rshell:printf \
  --allowed-command-patterns "ip route" \
  -p /tmp

# -----------------------------------------------------------------------
# 6. Substitution that resolves to a matching argv → allowed. Confirms
#    the matcher inspects the post-expansion argv rather than blanket-
#    rejecting interpolation.
# -----------------------------------------------------------------------
run_case "6. Substitution that matches the pattern (allowed)" \
  '$(printf ip) route show' \
  --allowed-commands rshell:printf \
  --allowed-command-patterns "ip route" \
  -p /tmp

# -----------------------------------------------------------------------
# 7. Union of name allowlist + pattern allowlist. printf is allowed by
#    name (any args). ip is only allowed when the argv contains "route".
#    Both invocations succeed — each finds its own permit.
# -----------------------------------------------------------------------
run_case "7. Union of name allowlist + pattern allowlist" \
  'printf "from-printf\n"; ip route show' \
  --allowed-commands rshell:printf \
  --allowed-command-patterns "ip route" \
  -p /tmp

# -----------------------------------------------------------------------
# 8. Pattern enforced through find -exec. find substitutes {} at
#    runtime; the eval-time gate sees the full resolved argv and
#    applies the pattern. Same security model, deeper call site.
# -----------------------------------------------------------------------
EXAMPLE_DIR=$(mktemp -d)
touch "$EXAMPLE_DIR/probe.txt"
run_case "8. find -exec respects the same patterns" \
  "find $EXAMPLE_DIR -name 'probe.txt' -exec echo hello {} \\;" \
  --allowed-commands rshell:find \
  --allowed-command-patterns "echo" \
  -p "/tmp,$EXAMPLE_DIR"
rm -rf "$EXAMPLE_DIR"

# -----------------------------------------------------------------------
# 9. Spec-driven matching closes the positional-arg bypass. Before
#    spec-aware classification, a pattern (echo, secret) would have
#    matched argv ["echo","public","secret"] because "secret" appears
#    anywhere in argv. With the spec-driven structural matcher (and
#    here echo treated as a positional-only command via an empty spec),
#    the matcher checks pattern[1] against the LEADING structural
#    token — which is "public", not "secret". Block.
#
#    Note: this case requires a spec for echo (provided via the rshell
#    CLI is not yet possible — operators using the library API call
#    interp.CommandSpecs(...) instead). The CLI uses the built-in
#    registry only, which today contains just `ip`. So this CLI
#    invocation actually fails at runner construction with a "no
#    registered CommandSpec" error, which is the right behaviour: the
#    operator is told their pattern is unsafe without a spec.
# -----------------------------------------------------------------------
run_case "9. Multi-token pattern without a registered spec → config error" \
  "echo public secret" \
  --allowed-command-patterns "echo secret" -p /tmp

# -----------------------------------------------------------------------
# 10. Name allowlist trumps pattern restriction. When a command is in
#     --allowed-commands, ALL its argv forms are admitted regardless of
#     patterns. To use prefix scoping, keep the command OUT of the name
#     allowlist. Here ip is allowed by name so the pattern (ip route)
#     doesn't restrict it; "ip addr show" runs even though argv lacks
#     the "route" token.
# -----------------------------------------------------------------------
run_case "10. Name allowlist overrides patterns" \
  "ip addr show" \
  --allowed-commands rshell:ip \
  --allowed-command-patterns "ip route" \
  -p /tmp

# -----------------------------------------------------------------------
# 11. Deny pattern overrides a name allowlist. Allow ip wholesale, but
#     carve out ip route specifically. ip addr admits; ip route refused.
# -----------------------------------------------------------------------
run_case "11. Deny pattern carves out a subcommand from a name allowlist" \
  "ip addr show" \
  --allowed-commands rshell:ip \
  --denied-command-patterns "ip route" \
  -p /tmp

run_case "12. Deny fires for the carved-out subcommand" \
  "ip route show" \
  --allowed-commands rshell:ip \
  --denied-command-patterns "ip route" \
  -p /tmp

# -----------------------------------------------------------------------
# 13. Deny is evaluated post-expansion (architectural test, deny axis).
#     A substitution that resolves to a denied argv at runtime is blocked
#     regardless of how opaque the literal command text was.
# -----------------------------------------------------------------------
run_case "13. Substitution can't bypass a deny pattern" \
  '$(printf ip) route show' \
  --allowed-commands rshell:ip,rshell:printf \
  --denied-command-patterns "ip route" \
  -p /tmp

printf "${BOLD}Done.${RESET} Edit examples/command_patterns.sh to add your own cases.\n"
