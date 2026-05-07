# Coverage Improvement Progress

Tracking progress of `/improve-test-coverage all` run started 2026-05-07.

## Target list (sorted by current test count, ascending)

Legend: ⏳ pending · 🔄 in progress · ✅ done · ⏭️ skipped (no high-value gaps)

| #  | Target              | Type  | Tests (before) | Tests (after) | Status | Notes |
|---:|---------------------|-------|---------------:|--------------:|--------|-------|
| 1  | readonly            | shell |              8 | 8             | ⏭️     | already comprehensive — 8 scenarios cover every variant of intentionally-blocked keyword |
| 2  | case_clause         | shell |             10 | 10            | ⏭️     | already comprehensive — 10 scenarios cover every pattern variant of intentionally-blocked syntax |
| 3  | errors              | shell |             10 | 10            | ⏭️     | already comprehensive — covers cmd-not-found in script/if/pipeline/cmd-subst plus exit-code propagation and recovery |
| 4  | function            | shell |             10 | 10            | ⏭️     | already comprehensive — 10 scenarios cover every variant of intentionally-blocked syntax |
| 5  | empty_script        | shell |             11 | 11            | ⏭️     | already comprehensive — covers empty/comments/whitespace/CRLF/tabs combinations |
| 6  | allowed_commands    | shell |             12 | 12            | ⏭️     | already comprehensive — covers allow_all, allow-list, blocked-in-pipe/subshell/cmdsubst, special builtins |
| 7  | subshell            | shell |             12 | 12            | ⏭️     | already comprehensive — covers basic/nested/triple-nested-isolation, exit, var-isolation, pipe, redirect, &&/\|\| |
| 8  | input_processing    | shell |             13 | 13            | ⏭️     | already comprehensive — covers blank lines, comments, whitespace handling, tabs/spaces, long lines, no-trailing-newline |
| 9  | blocked_redirects   | shell |             17 | 17            | ⏭️     | already comprehensive — covers every blocked redir form (>, >>, &>, &>>, 2>, <>, >\|, <&, >&, etc.) |
| 10 | allowed_redirects   | shell |             18 | 18            | ⏭️     | already comprehensive — input redir + heredoc combinations (pipes, &&, brace, for, multi-input, special chars) |
| 11 | inline_var          | shell |             18 | 18            | ⏭️     | already comprehensive — covers scope, restore, POSIX-order, pipeline, special chars, persistence-on-empty-cmd |
| 12 | command_substitution| shell |             20 | 20            | ⏭️     | already comprehensive — covers $() and ``, $(<file) shortcut, exit-status propagation, nesting, pipes, word splitting |
| 13 | heredoc_dash        | shell |             20 | 20            | ⏭️     | already comprehensive — covers tab-stripping, quoted-delim, blanks, nesting, brace, for, pipe, && |
| 14 | simple_command      | shell |             21 | 21            | ⏭️     | already comprehensive — covers assignments, multiple, expansion, quoting, persistence, overwrite, with cmd-subst |
| 15 | until_clause        | shell |             21 | 21            | ⏭️     | already comprehensive — covers loop semantics, break/continue (incl. multi-level), pipeline cond, brace body, nesting |
| 16 | break               | cmd   |             23 | 23            | ⏭️     | exhaustively covered by 77+ loop scenarios across for/while/until clauses |
| 17 | continue            | cmd   |             23 | 23            | ⏭️     | exhaustively covered by loop scenarios across for/while/until clauses |
| 18 | redirections        | shell |             26 | 26            | ⏭️     | already comprehensive — covers /dev/null target, heredoc variants, delimiter quoting, dup, multi-heredoc |
| 19 | uname               | cmd   |             27 | 27            | ⏭️     | already comprehensive — 24 Go tests + 3 scenarios cover every flag/combo/error/platform path |
| 20 | comments            | shell |             27 | 27            | ⏭️     | already comprehensive — covers # in/outside quotes, after operators, with backslash, after redirect, in pipelines |
| 21 | negation            | shell |             27 | 27            | ⏭️     | already comprehensive — covers ! on simple cmds, pipelines, in if-cond, with &&/\|\|, in else, with cmd-subst |
| 22 | line_continuation   | shell |             28 | 28            | ⏭️     | already comprehensive — covers backslash-newline across pipes, &&/\|\|, in assignments, in heredoc, multiple consecutive |
| 23 | while_clause        | shell |             28 | 28            | ⏭️     | already comprehensive — covers loop semantics, break/continue at all levels, pipeline-stage loop-context propagation |
| 24 | brace_group         | shell |             33 | 33            | ⏭️     | already comprehensive — covers {} groups in &&/\|\| chains, nesting, exit-code prop, with assign+exit |
| 25 | help                | cmd   |             34 | 34            | ⏭️     | already comprehensive — 31 Go tests + 10 scenarios cover restricted/unrestricted, --all, footer/header, alignment |
| 26 | ss                  | cmd   |             35 | 35            | ⏭️     | already comprehensive — 27 Go tests (incl. fuzz, linux, pentest) + 8 scenarios cover the surface |
| 27 | field_splitting     | shell |             37 | 37            | ⏭️     | already comprehensive — covers IFS variations, empty fields, special chars, prevents-glob, quoted preservation |
| 28 | ps                  | cmd   |             38 | 38            | ⏭️     | already comprehensive — 26 Go tests (incl. fuzz, linux proc-path) + 12 scenarios cover the surface |
| 29 | environment         | shell |             41 | 41            | ⏭️     | already comprehensive — covers IFS, $HOME, empty vs unset, Env option (override/special chars/empty/no-pollution) |
| 30 | false               | cmd   |             42 | 42            | ⏭️     | already overcomprehensive — false is a no-flag no-arg always-1 builtin; 19 scenarios more than enough |
| 31 | heredoc             | shell |             42 | 42            | ⏭️     | already comprehensive — covers basic, EOF/custom delimiters, expansion suppression, var/cmd-subst expansion, in for/&&/pipe |
| 32 | blocked_commands    | shell |             43 | 43            | ⏭️     | already comprehensive — covers every blocked syntactic construct (case/declare/eval/let/coproc/&/(()) etc.) |
| 33 | pwd                 | cmd   |             44 | 44            | ⏭️     | already comprehensive — 27 Go tests (incl. fuzz, pentest, internal symlink-loop) + 17 scenarios |
| 34 | true                | cmd   |             45 | 45            | ⏭️     | already overcomprehensive — true is a no-flag no-arg always-0 builtin; 22 scenarios more than enough |
| 35 | du                  | cmd   |             51 | 51            | ⏭️     | already comprehensive — 27 Go tests + 24 scenarios cover flags, units, errors, hardening |
| 36 | allowed_paths       | shell |             51 | 51            | ⏭️     | already comprehensive — covers sandbox path resolution, symlink escape, dot-dot, multiple paths, denials |
| 37 | cmd_separator       | shell |             52 | 52            | ⏭️     | already comprehensive — covers ;, &&, \|\|, newline, mixed, with comments, in groups |
| 38 | globbing            | shell |             52 | 52            | ⏭️     | already comprehensive — covers *, ?, [...], escaped glob chars, no-match, dotfiles, in for/word-list |
| 39 | pipe                | shell |             56 | 56            | ⏭️     | already comprehensive — covers basic, multi-stage, exit-status (last/negated), with cmd-subst, with redirs |
| 40 | ping                | cmd   |             59 | 59            | ⏭️     | already comprehensive — 26 Go tests + 33 scenarios cover flags, IPv4/IPv6, count/timeout, errors |
| 41 | strings             | cmd   |             62 | 62            | ⏭️     | already comprehensive — 25 Go tests + 37 scenarios cover -n/--bytes, encoding, binary input, errors |
| 42 | exit                | cmd   |             63 | 63            | ⏭️     | already comprehensive — 40 scenarios cover exit codes, status propagation, no-arg, in subshell/group |
| 43 | ip                  | cmd   |             67 | 67            | ⏭️     | already comprehensive — 26 Go tests (linux+pentest) + 41 scenarios cover ip route/addr/link |
| 44 | tr                  | cmd   |             68 | 68            | ⏭️     | already comprehensive — 27 Go tests + 41 scenarios cover translation, deletion, squeeze, classes |
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

- Targets processed: 44 / 64
- Tests added: 0 (scenario: 0, unit: 0)
- Duplicate tests removed: 0 (scenario: 0, unit: 0)
- Low-value tests removed: 0 (scenario: 0, unit: 0)
- `skip_assert_against_bash` flags removed: 0
- Windows-specific assertions removed: 0
