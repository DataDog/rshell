# Coverage Improvement Progress

Tracking progress of `/improve-test-coverage all` run started 2026-05-07.

## Target list (sorted by current test count, ascending)

Legend: ⏳ pending · 🔄 in progress · ✅ done · ⏭️ skipped (no high-value gaps)

| #  | Target              | Type  | Tests (before) | Tests (after) | Status | Notes |
|---:|---------------------|-------|---------------:|--------------:|--------|-------|
| 1  | readonly            | shell |              8 | —             | ⏳     |       |
| 2  | case_clause         | shell |             10 | —             | ⏳     |       |
| 3  | errors              | shell |             10 | —             | ⏳     |       |
| 4  | function            | shell |             10 | —             | ⏳     |       |
| 5  | empty_script        | shell |             11 | —             | ⏳     |       |
| 6  | allowed_commands    | shell |             12 | —             | ⏳     |       |
| 7  | subshell            | shell |             12 | —             | ⏳     |       |
| 8  | input_processing    | shell |             13 | —             | ⏳     |       |
| 9  | blocked_redirects   | shell |             17 | —             | ⏳     |       |
| 10 | allowed_redirects   | shell |             18 | —             | ⏳     |       |
| 11 | inline_var          | shell |             18 | —             | ⏳     |       |
| 12 | command_substitution| shell |             20 | —             | ⏳     |       |
| 13 | heredoc_dash        | shell |             20 | —             | ⏳     |       |
| 14 | simple_command      | shell |             21 | —             | ⏳     |       |
| 15 | until_clause        | shell |             21 | —             | ⏳     |       |
| 16 | break               | cmd   |             23 | —             | ⏳     |       |
| 17 | continue            | cmd   |             23 | —             | ⏳     |       |
| 18 | redirections        | shell |             26 | —             | ⏳     |       |
| 19 | uname               | cmd   |             27 | —             | ⏳     |       |
| 20 | comments            | shell |             27 | —             | ⏳     |       |
| 21 | negation            | shell |             27 | —             | ⏳     |       |
| 22 | line_continuation   | shell |             28 | —             | ⏳     |       |
| 23 | while_clause        | shell |             28 | —             | ⏳     |       |
| 24 | brace_group         | shell |             33 | —             | ⏳     |       |
| 25 | help                | cmd   |             34 | —             | ⏳     |       |
| 26 | ss                  | cmd   |             35 | —             | ⏳     |       |
| 27 | field_splitting     | shell |             37 | —             | ⏳     |       |
| 28 | ps                  | cmd   |             38 | —             | ⏳     |       |
| 29 | environment         | shell |             41 | —             | ⏳     |       |
| 30 | false               | cmd   |             42 | —             | ⏳     |       |
| 31 | heredoc             | shell |             42 | —             | ⏳     |       |
| 32 | blocked_commands    | shell |             43 | —             | ⏳     |       |
| 33 | pwd                 | cmd   |             44 | —             | ⏳     |       |
| 34 | true                | cmd   |             45 | —             | ⏳     |       |
| 35 | du                  | cmd   |             51 | —             | ⏳     |       |
| 36 | allowed_paths       | shell |             51 | —             | ⏳     |       |
| 37 | cmd_separator       | shell |             52 | —             | ⏳     |       |
| 38 | globbing            | shell |             52 | —             | ⏳     |       |
| 39 | pipe                | shell |             56 | —             | ⏳     |       |
| 40 | ping                | cmd   |             59 | —             | ⏳     |       |
| 41 | strings             | cmd   |             62 | —             | ⏳     |       |
| 42 | exit                | cmd   |             63 | —             | ⏳     |       |
| 43 | ip                  | cmd   |             67 | —             | ⏳     |       |
| 44 | tr                  | cmd   |             68 | —             | ⏳     |       |
| 45 | cat                 | cmd   |             71 | —             | ⏳     |       |
| 46 | read                | cmd   |             71 | —             | ⏳     |       |
| 47 | test                | cmd   |             71 | —             | ⏳     |       |
| 48 | if_clause           | shell |             71 | —             | ⏳     |       |
| 49 | uniq                | cmd   |             73 | —             | ⏳     |       |
| 50 | wc                  | cmd   |             76 | —             | ⏳     |       |
| 51 | logic_ops           | shell |             76 | —             | ⏳     |       |
| 52 | xargs               | cmd   |             78 | —             | ⏳     |       |
| 53 | cut                 | cmd   |             80 | —             | ⏳     |       |
| 54 | grep                | cmd   |             80 | —             | ⏳     |       |
| 55 | sort                | cmd   |             84 | —             | ⏳     |       |
| 56 | ls                  | cmd   |             86 | —             | ⏳     |       |
| 57 | echo                | cmd   |             87 | —             | ⏳     |       |
| 58 | head                | cmd   |             89 | —             | ⏳     |       |
| 59 | tail                | cmd   |            108 | —             | ⏳     |       |
| 60 | for_clause          | shell |            137 | —             | ⏳     |       |
| 61 | printf              | cmd   |            149 | —             | ⏳     |       |
| 62 | sed                 | cmd   |            170 | —             | ⏳     |       |
| 63 | var_expand          | shell |            172 | —             | ⏳     |       |
| 64 | find                | cmd   |            249 | —             | ⏳     |       |

## Summary

- Targets processed: 0 / 64
- Tests added: 0 (scenario: 0, unit: 0)
- Duplicate tests removed: 0 (scenario: 0, unit: 0)
- Low-value tests removed: 0 (scenario: 0, unit: 0)
- `skip_assert_against_bash` flags removed: 0
- Windows-specific assertions removed: 0
