---
name: datadog/remote-host-diagnostics
description: Load this skill when running diagnostic commands through the local ./rshell CLI.
toolsets: core
---

# Remote Host Diagnostics

One-line summary: Run safe, read-only diagnostics through the local `./rshell` CLI and produce evidence-grounded conclusions.

---

## Non-negotiables

- Use the Bash tool to run the repository-local `./rshell` binary. Do **not** use Datadog remote-action tools or imply that a real remote host was contacted.
- Keep all commands read-only. Do not write files, create directories, edit configs, restart/kill processes, or run remediation commands.
- Use `--allowed-paths <log-root>` for every command that reads logs. If the user provides a fake/generated/explicit log root, use that exact root instead of `/var/log`.
- In actual Bash commands, paste the prompt-provided log root literally after `--allowed-paths` and in file paths; do not hide it behind shell variables such as `$ROOT`. If quoting is needed, quote the literal path. In the final answer for large log investigations, avoid repeating long absolute generated roots; cite files relative to the provided root and use `<provided log root>` in command summaries.
- Keep reads bounded with `tail`, `head`, targeted `grep`, `wc`, `sort`, `uniq`, `find`, or command-specific filters. Do not dump whole large logs.

## Tools

### Bash with local `./rshell`

Run restricted-shell commands from the repository root:

```
./rshell --allow-all-commands --timeout 5s --allowed-paths <log-root> -c '<command>'
```

For non-filesystem checks such as command discovery or sockets, omit `--allowed-paths` unless a log path is read:

```
./rshell --allow-all-commands --timeout 5s -c 'help'
```

---

## Command Discovery and Flag Safety

The set of available commands and flags varies by rshell build.

1. Start every diagnostic session with:

   ```
   ./rshell --allow-all-commands --timeout 5s -c 'help'
   ```

2. Before using non-trivial or suggested flags, inspect command help, for example:

   ```
   ./rshell --allow-all-commands --timeout 5s -c 'help ss'
   ```

3. If help does not list a command or flag, treat it as unavailable. Do not run a teammate-suggested command just to prove it fails when help already shows unsupported flags.
4. If a command fails, explain the failure and choose a corrected command only after inspecting the error/help output; do not blindly retry the same command.

**Socket checks:** For listening TCP sockets, prefer supported commands such as `ss -tln` or `ss -tlnH` after checking `help ss`. Do not use `ss -p`, `ss -tulpn`, or `--process` unless `help ss` explicitly supports process/PID output. If process flags are unavailable, say that local listening addresses/ports can be collected but process names/PIDs cannot.

## Filesystem and Log Roots

The CLI only allows file access under directories passed to `--allowed-paths`.

- Real host default: `/var/log`.
- Fake/generated/explicit root from the prompt: use the prompt-provided path exactly.
- Start by listing the allowed root and nearby subdirectories to discover available logs.

**Containerized environments and host-mounted logs:** When an Agent runs in a container, host logs may be mounted under a separate host root (often `/host/var/log`, but use any explicit path from the prompt). If the primary log root is empty or returns "no such file", inspect the provided host-mounted root with its own `--allowed-paths` and continue there. In the final answer, mention that primary logs were empty and host-mounted fallback logs were used.

## Diagnostic Workflow

Use a small, evidence-driven loop:

1. **Discover:** run `help`, list the log root, and use `find`/`ls` to identify relevant files without reading them fully.
2. **Target:** search by the user's symptom, service names, time window, and common error terms. Prefer current logs first, then rotated logs only to confirm whether similar events are old/noisy.
3. **Correlate:** compare timestamps across service, agent, nginx/access/error, auth, system, database, and rotated logs as relevant. Separate temporally aligned evidence from red herrings.
4. **Quantify when useful:** use `wc -l`, `sort`, and `uniq -c` for counts (for example, repeated auth failures by source IP/user or status-code counts).
5. **Conclude carefully:** state the likely root cause or finding, cite filenames plus representative log snippets/timestamps, preserve useful `grep -n` line numbers and IDs (`transaction_id`, `request_id`, source IP, `application_name`, `line=<n>`), name what was ruled out, and give only safe read-only next diagnostic checks.

Useful bounded command patterns (adapt paths/keywords to the prompt and available logs):

```
./rshell --allow-all-commands --timeout 5s --allowed-paths <root> -c 'ls -la <root>'
./rshell --allow-all-commands --timeout 5s --allowed-paths <root> -c 'find <root> -maxdepth 3 -type f | head -n 80'
./rshell --allow-all-commands --timeout 5s --allowed-paths <root> -c 'grep -n -E "(ERROR|WARN|failed|timeout|500|502|database|postgres|x509|clock|config|yaml|metrics)" <file> | head -n 80'
./rshell --allow-all-commands --timeout 5s --allowed-paths <root> -c 'grep -n "<time-or-id>" <file> | head -n 80'
```

## Case Patterns to Handle Well

These are general diagnostic patterns, not facts to assume. Always verify with logs before concluding.

- **Datadog Agent metric stoppage:** inspect `datadog/agent.log` and rotations for config reloads, remote-config events, YAML/validation errors, aggregator/core-agent stoppage, forwarder/flush messages, and trace/APM/log-intake health. In the finding, report the exact validation error and any line number/error field (`line=<n>`), transaction/config ID, and stop/flush timestamps as primary evidence. If traces or log intake remain healthy, explicitly separate them from a metrics-agent failure.
- **SSH brute-force investigations:** search `auth.log*` for `Failed password`, invalid users, source IPs, and `Accepted` logins. Count failures by source and user. If there are no `Accepted` lines from the suspicious source, write plainly: "No accepted login from <source> was found." Prefer `accepted`/`Accepted` wording over placing `successful` next to the suspicious IP. If accepted logins exist from other sources, explicitly include the source IP and auth method (for example, `Accepted publickey`) so they are not confused with the suspicious source. Unless an accepted login from the suspicious source is evidenced, avoid the words "compromise" and "compromised" entirely.
- **HTTP 500/502 app/backend incidents:** correlate access/error logs with app/service logs and system/database logs around the same window. Look for database/Postgres connection errors, pool exhaustion, worker fanout, timeouts, and upstream failures. Preserve request IDs, line numbers, status counts, and application names when available. Recommend only read-only next checks such as inspecting connection-pool metrics or `pg_stat_activity`; do not mention operational changes in the final answer.
- **Certificate failures in containers:** if primary logs are empty, use host-mounted fallback logs. For `x509` errors, distinguish expired certificates from `not yet valid`/NotBefore problems, and corroborate timing causes with syslog/chrony/clock messages when present. Keep evidence attributed to the file where it appeared (for example, Agent check failures in `agent.log`, clock synchronization in `syslog`).

## Final Answer Checklist

Your final answer should be concise but complete:

- Use a clear structure: **Finding**, **Evidence by file**, **Commands run**, **Ruled out/noise**, and **Next safe read-only check**.
- Start with the likely finding/root cause and confidence level.
- Cite concrete evidence: filenames, line numbers from `grep -n` when available, timestamps, key terms, counts, IDs, and short snippets. Prefer exact file names like `agent.log`, `auth.log`, `service.log`, `nginx/access.log`, `nginx/error.log`, `system.log`, or `syslog` when used.
- List the important `./rshell` commands you ran. When there are only a few commands (for example socket checks), list every command exactly. For larger log investigations, list the initial `help` command exactly, then give representative decisive command forms with `<provided log root>`, relative file names, and the actual search/count patterns used (not just vague "targeted grep" categories); avoid copying long absolute generated roots into the final answer. Make clear that the actual log reads used the literal provided `--allowed-paths` root.
- Separate confirmed evidence from red herrings/older rotated-log noise.
- Use neutral negative findings: say "No accepted login from <source> was found" instead of "not compromised" unless an accepted login from that source is directly evidenced; avoid phrasing like "successful ... <source>" when the finding is negative.
- State limitations and the next safe read-only diagnostic check.
- Do not include operational-change command names in the final answer, even in negative phrasing; just state that the next checks are read-only.
- Do not claim remote-host access or describe the tool as a "skill" in the final answer; describe the investigation as using local `./rshell` against the provided log root.

## Examples

```
# View recent syslog errors
./rshell --allow-all-commands --timeout 5s --allowed-paths /var/log -c 'tail -n 50 /var/log/syslog | grep -i error'

# List available log files
./rshell --allow-all-commands --timeout 5s --allowed-paths /var/log -c 'ls -la /var/log'

# Check supported listening TCP sockets after help/help ss
./rshell --allow-all-commands --timeout 5s -c 'ss -tln'
```
