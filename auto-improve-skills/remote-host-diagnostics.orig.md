---
name: datadog/remote-host-diagnostics
description: Load this skill when running diagnostic commands on customer hosts through the Datadog Agent using a restricted shell (rshell).
toolsets: core, remote-actions
---

# Remote Host Diagnostics

One-line summary: Run diagnostic commands on customer hosts through the Datadog Agent restricted shell (rshell).

---

## Tools

### datadog_remote_action_restricted_shell_run_command

Run shell commands on a customer's host via the Datadog Agent restricted shell. Commands execute in a sandboxed interpreter with a curated set of read-only commands and filesystem access limited to `/var/log`.

| Parameter | Required | Description |
|---|---|---|
| `command` | Yes | Shell command to run. Pipes (`|`) and standard POSIX constructs supported. |
| `hostname` | No* | The hostname of the machine to run the command on. Preferred over `connection_id` — the tool resolves it to a PAR connection automatically. |
| `connection_id` | No* | Private Action Runner connection ID targeting the Datadog Agent on the host to inspect. Use when hostname resolution is unavailable. |

*One of `hostname` or `connection_id` is required. Prefer `hostname` when the user provides a host identifier — the tool will resolve it to the correct PAR connection. Only ask for `connection_id` if hostname resolution fails or the user explicitly provides one.

---

## Available Commands

The set of available commands varies by Datadog Agent version. Always run `help` first to discover exactly which commands are available on the target runner:

```
help
```

Do not assume a command exists — if `help` does not list it, it is not available and will return exit code 127 (command not found).

Run `help` at the start of every new diagnostic session, even if you have used the tool before. The command list may have changed between agent versions.

## Filesystem Access

Only `/var/log` and its subdirectories are accessible. All other paths are blocked.

**Containerized environments:** When the Datadog Agent runs in a container, host filesystem paths are mounted under `/host`. For example, `/var/log` on the host becomes `/host/var/log` inside the container. If commands against `/var/log` return empty results or "no such file" errors, retry under `/host/var/log`. When in doubt, check both paths.

Start by listing the contents of `/var/log` to discover what logs are available on the host.

## Examples

```
# View recent syslog errors (using hostname — preferred)
datadog_remote_action_restricted_shell_run_command(
  command="tail -n 50 /var/log/syslog | grep -i error",
  hostname="<hostname>"
)

# List available log files (using hostname)
datadog_remote_action_restricted_shell_run_command(
  command="ls -la /var/log",
  hostname="<hostname>"
)

# Check network connectivity (using connection_id)
datadog_remote_action_restricted_shell_run_command(
  command="ss -tlnp",
  connection_id="<connection-id>"
)
```

## Best Practices

- Always run `help` first to discover available commands
- Use `tail`, `head`, or `grep` to limit output — never `cat` an entire large log file without filtering
- Read-only: no file writes, directory creation, or host modifications. Output redirections work only to `/dev/null`
- Do not rely on standard environment variables like `$HOME` or `$PATH` — the shell runs with a minimal environment
- Report errors clearly: if a command returns a non-zero exit code, explain the failure to the user. Do not retry the same failing command without understanding why it failed
- Explain your actions: tell the user what command you are about to run and why. After getting results, interpret them in the context of the user's question
