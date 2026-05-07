# AWK External Test Harness

This harness runs rshell's future `awk` implementation against upstream AWK
test suites without vendoring those suites into this Apache-licensed
repository.

The compatibility oracle is a pinned GNU awk (`gawk`) binary installed into the
harness cache. The harness never uses macOS `/usr/bin/awk`, mawk, BusyBox awk,
a distro-provided `gawk` with a different version, or a built One True Awk
binary as the reference implementation.

The harness fetches upstream repositories into a platform-specific directory
under `.superset/awk-harness` by default, such as
`.superset/awk-harness/darwin-arm64` or `.superset/awk-harness/linux-x86_64`.
That directory is ignored by git.

## Upstreams

- One True Awk: `https://github.com/onetrueawk/awk.git`
- GNU awk: `https://git.savannah.gnu.org/git/gawk.git`
- GNU awk release tarballs: `https://ftp.gnu.org/gnu/gawk/`

One True Awk is permissively licensed, but we still fetch it externally so CI
can test against a pinned upstream ref without recurring test-sync PRs. GNU awk
is GPL-family licensed and must not be copied into this repository.

## Installing The GNU awk Oracle

Install the pinned oracle before running comparisons:

```bash
tools/awk-harness/run.sh install-gawk
```

By default this builds GNU awk `5.4.0` from the official GNU release tarball and
installs it under:

```text
.superset/awk-harness/<platform>/oracle/gawk-5.4.0/bin/gawk
```

The installer can install build dependencies on supported systems:

- macOS: Homebrew dependencies `gmp`, `mpfr`, `readline`, and `gettext`
- Ubuntu/Debian: `build-essential`, `curl`, `tar`, `libgmp-dev`, `libmpfr-dev`,
  `libreadline-dev`, and `gettext`

Override the pinned version when needed:

```bash
GAWK_VERSION=5.4.0 tools/awk-harness/run.sh install-gawk
```

Override the oracle binary only when it is the same pinned version:

```bash
GAWK_ORACLE=/opt/gawk-5.4.0/bin/gawk tools/awk-harness/run.sh gawk
```

The harness rejects a `GAWK_ORACLE` whose `gawk --version` does not match
`GAWK_VERSION`.

## Usage

Run the rshell-owned rewritten AWK scenarios against rshell's `awk` adapter:

```bash
tools/awk-harness/run.sh install-gawk
make build
RSHELL_BIN=./rshell AWK_UNDER_TEST=tools/awk-harness/rshell-awk tools/awk-harness/run.sh rewritten
```

Check whether the rewrite ledger accounts for every fetched upstream test:

```bash
tools/awk-harness/run.sh check-rewrite-map
```

When upstream refs change, or when bootstrapping the ledger, append missing
entries as explicit `todo` work:

```bash
tools/awk-harness/run.sh sync-rewrite-map
```

Point `AWK_UNDER_TEST` at the candidate binary to test:

```bash
AWK_UNDER_TEST=/path/to/awk tools/awk-harness/run.sh rewritten
AWK_UNDER_TEST=/path/to/awk tools/awk-harness/run.sh gawk
AWK_UNDER_TEST=/path/to/awk tools/awk-harness/run.sh onetrueawk
AWK_UNDER_TEST=/path/to/awk tools/awk-harness/run.sh all
```

For rshell, use the adapter that turns awk argv into an rshell `-c` command:

```bash
make build
RSHELL_BIN=./rshell AWK_UNDER_TEST=tools/awk-harness/rshell-awk tools/awk-harness/run.sh rewritten
RSHELL_BIN=./rshell AWK_UNDER_TEST=tools/awk-harness/rshell-awk tools/awk-harness/run.sh all
```

The adapter defaults to:

```bash
./rshell --allow-all-commands --allowed-paths / -c 'awk ...'
```

Override the binary or allowed paths when needed:

```bash
RSHELL_BIN=/path/to/rshell RSHELL_ALLOWED_PATHS=/tmp,/var/tmp AWK_UNDER_TEST=tools/awk-harness/rshell-awk tools/awk-harness/run.sh gawk
```

To validate fetching and test discovery before rshell has an `awk` builtin:

```bash
AWK_HARNESS_BOOTSTRAP=1 tools/awk-harness/run.sh all
```

Bootstrap mode still requires the pinned GNU awk oracle, because the gawk source
ref and comparison semantics are tied to `GAWK_VERSION`.

## Refs And Cache

`GAWK_VERSION` defaults to `5.4.0`, and `GAWK_REF` defaults to
`gawk-$GAWK_VERSION`. Override them together only for deliberate experiments:

```bash
GAWK_VERSION=5.4.0 GAWK_REF=gawk-5.4.0 AWK_HARNESS_BOOTSTRAP=1 tools/awk-harness/run.sh gawk
ONETRUEAWK_REF=master AWK_HARNESS_BOOTSTRAP=1 tools/awk-harness/run.sh onetrueawk
```

Use a different cache directory with:

```bash
AWK_HARNESS_CACHE=/tmp/rshell-awk-harness tools/awk-harness/run.sh fetch
```

Override the platform segment only when sharing cache policy deliberately:

```bash
AWK_HARNESS_PLATFORM=linux-x86_64 tools/awk-harness/run.sh install-gawk
```

## One True Awk Suites

`ONETRUEAWK_SUITE=core` runs `t`, `p`, and `T` suites. This is the default.
The suite scripts are run with pinned GNU awk as `oldawk` and `AWK_UNDER_TEST`
as the candidate.

`ONETRUEAWK_SUITE=all` also runs `tt` timing tests.

You can run individual suites with:

```bash
ONETRUEAWK_SUITE=t AWK_UNDER_TEST=/path/to/awk tools/awk-harness/run.sh onetrueawk
ONETRUEAWK_SUITE=t,p AWK_UNDER_TEST=/path/to/awk tools/awk-harness/run.sh onetrueawk
```

## Gawk Suites

`GAWK_TEST_MODE=triples` is the default. It runs gawk `test/*.awk` files that
have matching `.ok` files, using a sibling `.in` file when present. The `.ok`
file is used only to identify simple triplet tests; expected stdout, stderr,
and exit code are generated by the pinned GNU awk oracle for each run.

Useful filters:

```bash
GAWK_TEST_FILTER=split AWK_UNDER_TEST=/path/to/awk tools/awk-harness/run.sh gawk
GAWK_TEST_LIMIT=25 AWK_UNDER_TEST=/path/to/awk tools/awk-harness/run.sh gawk
```

`GAWK_TEST_MODE=make-check` is available for experiments with gawk's native
test harness. It may require GNU build tools and is not the default.

## Rewritten Local Scenarios

`tools/awk-harness/run.sh rewritten` runs the local AWK scenario rewrites listed
in `tests/awk_scenarios/enabled.txt`. These files are rshell-owned tests, not
vendored upstream tests. Each scenario carries upstream metadata and a `covers`
list so we can track which GNU awk or One True Awk behavior it rewrites.

`tests/awk_scenarios/upstream-map.yaml` is an audit ledger for upstream rewrite
coverage. It is not a run list; `enabled.txt` is the single source of truth for
which rewritten tests execute.

The ledger uses these statuses:

- `rewritten`: represented by original rshell-owned AWK scenario tests.
- `policy`: represented by rshell safety or integration scenarios instead of
  GNU-compatible success behavior.
- `deferred`: deliberately postponed with a reason.
- `todo`: fetched upstream test is accounted for, but an original rewrite has
  not been written yet.

`rewritten` requires `AWK_UNDER_TEST`. CI points it at
`tools/awk-harness/rshell-awk` so enabled scenarios are implementation gates for
rshell's awk. The pinned GNU awk binary is still required, but only as the
trusted comparison oracle.

## Outputs

Results and logs are written under:

```text
.superset/awk-harness/<platform>/results/
```

Each upstream writes a `summary.json` with the resolved commit SHA and counts.
