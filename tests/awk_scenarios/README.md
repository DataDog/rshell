# AWK Scenario Rewrites

This directory contains rshell-owned AWK tests rewritten from upstream behavior
coverage. Do not copy upstream test bodies, helper scripts, comments, fixtures,
or expected output into this directory.

Each scenario is a small GNU awk behavior case with metadata that identifies
which upstream suite or coverage area it belongs to and what behavior it covers.
The tests run through the AWK-specific Go runner in
`tests/awk_scenarios_test.go`.

`enabled.txt` is the only run list. Each non-comment line is a path relative to
this directory:

```text
gawk/basic/begin_end_records.yaml
onetrueawk/basic/pattern_action.yaml
```

`upstream-map.yaml` is an audit ledger for rewrite progress. It does not decide
which tests run.

Run the rewritten scenarios against the pinned GNU awk oracle:

```bash
tools/awk-harness/run.sh install-gawk
tools/awk-harness/run.sh rewritten
```

Run the same scenarios against rshell's `awk` adapter once the builtin exists:

```bash
make build
RSHELL_BIN=./rshell AWK_UNDER_TEST=tools/awk-harness/rshell-awk tools/awk-harness/run.sh rewritten
```

The runner still compares rshell output to the pinned GNU awk oracle when
`GAWK_ORACLE` is set, so expected output in these files and live GNU awk
behavior must stay aligned.
