---
name: datadog/remote-host-diagnostics
description: Load this skill when running diagnostic commands through the local ./rshell CLI.
toolsets: core
---

# Remote Host Diagnostics

One-line summary: Run diagnostic commands through the local ./rshell CLI.

---

## Tools

### Bash with local `./rshell`

Run restricted-shell commands with the Bash tool from the repository root:

```
./rshell --allow-all-commands --timeout 5s --allowed-paths <log-root> -c '<command>'
```

Use `--allowed-paths <log-root>` whenever reading logs. If the user provides a fake or explicit log root, use that root rather than `/var/log`. Keep commands read-only and bounded.

---

## Available Commands

The set of available commands varies by rshell build. Always run `help` first to discover exactly which commands are available:

```
./rshell --allow-all-commands --timeout 5s -c 'help'
```

Do not assume a command exists — if `help` does not list it, it is not available and will return exit code 127 (command not found).

Run `help` at the start of every new diagnostic session, even if you have used the CLI before. The command list may have changed between rshell builds.

## Filesystem Access

The CLI only allows file access under directories passed to `--allowed-paths`. For real host logs, use `/var/log`; if a fake or explicit log root is provided, use that root. All other paths are blocked.

**Containerized environments:** When the Datadog Agent runs in a container, host filesystem paths are mounted under `/host`. For example, `/var/log` on the host becomes `/host/var/log` inside the container. If commands against the primary log root return empty results or "no such file" errors, retry with the host-mounted log root. When in doubt, check both paths.

Start by listing the contents of the allowed log root to discover what logs are available.

## Examples

```
# View recent syslog errors
./rshell --allow-all-commands --timeout 5s --allowed-paths /var/log -c 'tail -n 50 /var/log/syslog | grep -i error'

# List available log files
./rshell --allow-all-commands --timeout 5s --allowed-paths /var/log -c 'ls -la /var/log'

# Check listening TCP sockets
./rshell --allow-all-commands --timeout 5s -c 'ss -tln'
```

## Best Practices

- Always run `help` first to discover available commands
- Use `tail`, `head`, or `grep` to limit output — never `cat` an entire large log file without filtering
- Read-only: no file writes, directory creation, or host modifications. Output redirections work only to `/dev/null`
- Do not rely on standard environment variables like `$HOME` or `$PATH` — the shell runs with a minimal environment
- Report errors clearly: if a command returns a non-zero exit code, explain the failure to the user. Do not retry the same failing command without understanding why it failed
- Explain your actions: tell the user what command you are about to run and why. After getting results, interpret them in the context of the user's question
