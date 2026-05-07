# Coverage Improvement Progress

Tracking progress of `/improve-test-coverage all` run started 2026-05-07.

## Target list (sorted by current test count, ascending)

Legend: ⏳ pending · 🔄 in progress · ✅ done · ⏭️ skipped (no high-value gaps)

| # | Target | Type | Tests (before) | Tests (after) | Status | Notes |
|---|--------|------|---------------:|--------------:|--------|-------|
| 1 | break | cmd | 0 | 0 | ⏭️ | 69 break-related scenarios across loop/if/logic_ops dirs cover all behavior |
| 2 | continue | cmd | 0 | 0 | ⏭️ | 62 continue-related scenarios across loop/if dirs cover all behavior |
| 3 | readonly | shell | 8 | 8 | ⏭️ | parse-block + 3 Go runtime API tests cover the surface |
| 4 | subshell | shell | 8 | 12 | ✅ | added 4 P2 tests (multiline, pipe-target, triple-nested isolation, input redirect) |
| 5 | empty_script | shell | 9 | 11 | ✅ | added 2 P2 tests for CRLF (Windows-saved) scripts |
| 6 | case_clause | shell | 10 | 10 | ⏭️ | case is intentionally blocked at parse validation; all syntactic forms covered |
| 7 | errors | shell | 10 | 10 | ✅ | replaced misnamed dupe with cmd-subst error test; framework parses scripts so syntax-error tests can't be scenarios |
| 8 | function | shell | 10 | 10 | ⏭️ | function declarations intentionally blocked at parse; all syntactic forms covered |
| 9 | allowed_commands | shell | 12 | 12 | ⏭️ | recently improved (commit fc86a688); 12 scenarios + 8 Go API tests cover surface |
| 10 | input_processing | shell | 13 | 13 | ⏭️ | comprehensive coverage of blanks/comments/whitespace/tabs/multi-cmd; minor overlap with empty_script accepted |
| 11 | allowed_redirects | shell | 17 | — | ⏳ | |
| 12 | blocked_redirects | shell | 17 | — | ⏳ | |
| 13 | inline_var | shell | 18 | — | ⏳ | |
| 14 | false | cmd | 20 | — | ⏳ | |
| 15 | command_substitution | shell | 20 | — | ⏳ | |
| 16 | heredoc_dash | shell | 20 | — | ⏳ | |
| 17 | simple_command | shell | 21 | — | ⏳ | |
| 18 | until_clause | shell | 21 | — | ⏳ | |
| 19 | true | cmd | 23 | — | ⏳ | |
| 20 | redirections | shell | 26 | — | ⏳ | |
| 21 | negation | shell | 27 | — | ⏳ | |
| 22 | comments | shell | 28 | — | ⏳ | |
| 23 | line_continuation | shell | 28 | — | ⏳ | |
| 24 | while_clause | shell | 28 | — | ⏳ | |
| 25 | uname | cmd | 32 | — | ⏳ | recently audited (Go tests cover surface) |
| 26 | brace_group | shell | 35 | — | ⏳ | |
| 27 | field_splitting | shell | 37 | — | ⏳ | |
| 28 | environment | shell | 41 | — | ⏳ | |
| 29 | help | cmd | 41 | — | ⏳ | |
| 30 | heredoc | shell | 42 | — | ⏳ | |
| 31 | exit | cmd | 44 | — | ⏳ | |
| 32 | strings_cmd | cmd | 44 | — | ⏳ | |
| 33 | blocked_commands | shell | 44 | — | ⏳ | |
| 34 | ps | cmd | 44 | — | ⏳ | |
| 35 | read | cmd | 50 | — | ⏳ | |
| 36 | allowed_paths | shell | 51 | — | ⏳ | |
| 37 | cmd_separator | shell | 52 | — | ⏳ | |
| 38 | globbing | shell | 52 | — | ⏳ | |
| 39 | pipe | shell | 57 | — | ⏳ | |
| 40 | if_clause | shell | 71 | — | ⏳ | |
| 41 | ss | cmd | 75 | — | ⏳ | |
| 42 | logic_ops | shell | 76 | — | ⏳ | |
| 43 | pwd | cmd | 96 | — | ⏳ | |
| 44 | ping | cmd | 97 | — | ⏳ | |
| 45 | ls | cmd | 103 | — | ⏳ | |
| 46 | du | cmd | 107 | — | ⏳ | |
| 47 | echo | cmd | 127 | — | ⏳ | |
| 48 | wc | cmd | 134 | — | ⏳ | |
| 49 | tr | cmd | 137 | — | ⏳ | |
| 50 | for_clause | shell | 137 | — | ⏳ | |
| 51 | sort | cmd | 141 | — | ⏳ | |
| 52 | cut | cmd | 144 | — | ⏳ | |
| 53 | ip | cmd | 144 | — | ⏳ | |
| 54 | cat | cmd | 145 | — | ⏳ | |
| 55 | grep | cmd | 151 | — | ⏳ | |
| 56 | uniq | cmd | 161 | — | ⏳ | |
| 57 | testcmd | cmd | 168 | — | ⏳ | |
| 58 | var_expand | shell | 172 | — | ⏳ | |
| 59 | xargs | cmd | 193 | — | ⏳ | |
| 60 | head | cmd | 194 | — | ⏳ | |
| 61 | tail | cmd | 213 | — | ⏳ | |
| 62 | sed | cmd | 261 | — | ⏳ | |
| 63 | printf | cmd | 301 | — | ⏳ | |
| 64 | find | cmd | 305 | — | ⏳ | |

## Summary

- Targets processed: 10 / 64
- Tests added: 7 (scenario: 7, unit: 0)
- Duplicate tests removed: 1 (scenario: 1, unit: 0)
- Low-value tests removed: 0 (scenario: 0, unit: 0)
- `skip_assert_against_bash` flags removed: 0
- Windows-specific assertions removed: 0
