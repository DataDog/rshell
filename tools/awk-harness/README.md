# AWK Scenario Harness

This harness runs rshell-owned GNU awk scenario rewrites against rshell's future
`awk` implementation. The scenarios live in this repository; the harness no
longer fetches GNU awk, One True Awk, or BWK test repositories.

The compatibility oracle is a pinned GNU awk (`gawk`) binary installed into the
harness cache. The oracle is used only to compare behavior for enabled local
scenarios. It must not be replaced by macOS `/usr/bin/awk`, mawk, BusyBox awk,
a distro-provided `gawk` with a different version, or a built One True Awk
binary.

## Installing The GNU awk Oracle

Install the pinned oracle before running rewritten scenarios:

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
- Ubuntu/Debian: `build-essential`, `ca-certificates`, `curl`, `tar`,
  `libgmp-dev`, `libmpfr-dev`, `libreadline-dev`, and `gettext`

Override the pinned version only for deliberate experiments:

```bash
GAWK_VERSION=5.4.0 tools/awk-harness/run.sh install-gawk
```

Override the oracle binary only when it is the same pinned version:

```bash
GAWK_ORACLE=/opt/gawk-5.4.0/bin/gawk tools/awk-harness/run.sh rewritten
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

For a focused metadata check that does not execute scenarios:

```bash
go test ./tests -run TestAwkScenarioMetadata -count=1
```

The adapter turns awk argv into an rshell `-c` command:

```bash
./rshell --allow-all-commands --allowed-paths / -c 'awk ...'
```

Override the rshell binary or allowed paths when needed:

```bash
RSHELL_BIN=/path/to/rshell RSHELL_ALLOWED_PATHS=/tmp,/var/tmp AWK_UNDER_TEST=tools/awk-harness/rshell-awk tools/awk-harness/run.sh rewritten
```

## Targets

- `install-gawk`: Build or reuse the pinned GNU awk oracle.
- `rewritten`: Run enabled local scenario rewrites against `AWK_UNDER_TEST` and
  compare them with the pinned GNU awk oracle.

## Rewritten Local Scenarios

`tools/awk-harness/run.sh rewritten` runs the local AWK scenario rewrites listed
in `tests/awk_scenarios/enabled.txt`. These files are rshell-owned tests, not
vendored upstream tests. Each scenario carries upstream metadata and a `covers`
list so we can track which GNU awk or One True Awk behavior it rewrites.

`enabled.txt` is intentionally empty until rshell has enough GNU awk
implementation to pass specific scenarios. Add one relative path per line as
features land.

`tests/awk_scenarios/upstream-map.yaml` is a local audit ledger for rewrite
coverage. It is not checked against external upstream repositories and it does
not decide which tests run.

The ledger uses these statuses:

- `rewritten`: represented by original rshell-owned AWK scenario tests.
- `policy`: represented by rshell safety or integration scenarios instead of
  GNU-compatible success behavior.
- `deferred`: deliberately postponed with a reason.
- `todo`: accounted-for coverage that has not been rewritten yet.

## Cache

Use a different cache directory with:

```bash
AWK_HARNESS_CACHE=/tmp/rshell-awk-harness tools/awk-harness/run.sh install-gawk
```

Override the platform segment only when sharing cache policy deliberately:

```bash
AWK_HARNESS_PLATFORM=linux-x86_64 tools/awk-harness/run.sh install-gawk
```
