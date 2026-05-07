# Coverage Improvement Progress

Tracking progress of `/improve-test-coverage all` run started 2026-05-07.

## Target list (sorted by current test count, ascending)

Legend: ⏳ pending · 🔄 in progress · ✅ done · ⏭️ skipped (no high-value gaps)

| # | Target | Type | Tests (before) | Tests (after) | Status | Notes |
|---|--------|------|---------------:|--------------:|--------|-------|
| 1 | uname | cmd | 3 | 3 | ⏭️ | scenario tests can't mock `/proc`; Go tests cover the surface (28 tests with fake proc) |
| 2 | allowed_commands | shell | 6 | — | ⏳ | |
| 3 | readonly | shell | 8 | — | ⏳ | |
| 4 | ss | cmd | 8 | — | ⏳ | |
| 5 | subshell | shell | 8 | — | ⏳ | |
| 6 | empty_script | shell | 9 | — | ⏳ | |
| 7 | case_clause | shell | 10 | — | ⏳ | |
| 8 | errors | shell | 10 | — | ⏳ | |
| 9 | function | shell | 10 | — | ⏳ | |
| 10 | help | cmd | 10 | — | ⏳ | |
| 11 | ps | cmd | 12 | — | ⏳ | |
| 12 | input_processing | shell | 13 | — | ⏳ | |
| 13 | allowed_redirects | shell | 17 | — | ⏳ | |
| 14 | blocked_redirects | shell | 17 | — | ⏳ | |
| 15 | pwd | cmd | 17 | — | ⏳ | |
| 16 | inline_var | shell | 18 | — | ⏳ | |
| 17 | command_substitution | shell | 20 | — | ⏳ | |
| 18 | false | cmd | 20 | — | ⏳ | |
| 19 | heredoc_dash | shell | 20 | — | ⏳ | |
| 20 | simple_command | shell | 21 | — | ⏳ | |
| 21 | until_clause | shell | 21 | — | ⏳ | |
| 22 | true | cmd | 23 | — | ⏳ | |
| 23 | du | cmd | 24 | — | ⏳ | |
| 24 | redirections | shell | 26 | — | ⏳ | |
| 25 | negation | shell | 27 | — | ⏳ | |
| 26 | comments | shell | 28 | — | ⏳ | |
| 27 | line_continuation | shell | 28 | — | ⏳ | |
| 28 | unknown_cmd | cmd | 28 | — | ⏳ | |
| 29 | while_clause | shell | 28 | — | ⏳ | |
| 30 | ping | cmd | 33 | — | ⏳ | |
| 31 | brace_group | shell | 35 | — | ⏳ | |
| 32 | field_splitting | shell | 37 | — | ⏳ | |
| 33 | strings | cmd | 37 | — | ⏳ | |
| 34 | environment | shell | 41 | — | ⏳ | |
| 35 | ip | cmd | 41 | — | ⏳ | |
| 36 | tr | cmd | 41 | — | ⏳ | |
| 37 | heredoc | shell | 42 | — | ⏳ | |
| 38 | test | cmd | 43 | — | ⏳ | |
| 39 | blocked_commands | shell | 44 | — | ⏳ | |
| 40 | cat | cmd | 44 | — | ⏳ | |
| 41 | exit | cmd | 44 | — | ⏳ | |
| 42 | uniq | cmd | 46 | — | ⏳ | |
| 43 | read | cmd | 47 | — | ⏳ | |
| 44 | wc | cmd | 47 | — | ⏳ | |
| 45 | xargs | cmd | 50 | — | ⏳ | |
| 46 | allowed_paths | shell | 51 | — | ⏳ | |
| 47 | cmd_separator | shell | 52 | — | ⏳ | |
| 48 | globbing | shell | 52 | — | ⏳ | |
| 49 | cut | cmd | 53 | — | ⏳ | |
| 50 | grep | cmd | 54 | — | ⏳ | |
| 51 | sort | cmd | 56 | — | ⏳ | |
| 52 | pipe | shell | 57 | — | ⏳ | |
| 53 | head | cmd | 60 | — | ⏳ | |
| 54 | ls | cmd | 60 | — | ⏳ | |
| 55 | echo | cmd | 62 | — | ⏳ | |
| 56 | if_clause | shell | 71 | — | ⏳ | |
| 57 | logic_ops | shell | 76 | — | ⏳ | |
| 58 | tail | cmd | 79 | — | ⏳ | |
| 59 | printf | cmd | 123 | — | ⏳ | |
| 60 | for_clause | shell | 137 | — | ⏳ | |
| 61 | sed | cmd | 143 | — | ⏳ | |
| 62 | var_expand | shell | 172 | — | ⏳ | |
| 63 | find | cmd | 219 | — | ⏳ | |

## Summary

- Targets processed: 1 / 63
- Total tests added: 0
- Duplicate tests removed: 0
- `skip_assert_against_bash` flags removed: 0
- Windows-specific assertions removed: 0
