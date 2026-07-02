# System Service And Journal Config Research

## Context

`candidates.md` accepts narrow `systemctl` and `journalctl` builtins because the
remediation scenarios repeatedly need service state, service control, journald
logs, kernel journal evidence, and journald disk recovery.

The key design problem is that these commands expose or mutate host state that
is not governed by `AllowedPaths`:

- `AllowedCommands` controls whether a builtin may run at all.
- `AllowedPaths` controls user-supplied filesystem access.
- `ModeRemediation` controls whether write-capable behavior is available.
- None of the existing knobs can express "this service may be restarted" or
  "journald vacuum is allowed".

The research goal was to find the smallest additional config surface that keeps
rshell useful for scenario-driven AI agents without silently expanding host
visibility or host mutation.

## Scenario Evidence

The scenarios require both service-scoped operations and a small set of
unscoped read-only discovery operations.

| Need | Scenario evidence | Config implication |
| --- | --- | --- |
| Inspect and control a known service | `systemctl status <service>`, `restart`, `stop`, `start`, `reload` appear across crash-loop, runaway CPU, memory leak, cache growth, GC pressure, Docker, and log scenarios. | Needs exact service policy plus verb-level control. |
| Read logs for a known service | `journalctl -u <service> -n ...` and `journalctl -u <service> --since ...` appear in crash-loop, cron storm, and core dump workflows. | Service read policy should authorize unit-scoped journal reads. |
| Discover failed units | `systemctl --state=failed` is used to identify crash-looping units. | This is unscoped read-only host discovery. |
| Discover maintenance timers | `systemctl list-timers | grep ...` is used for logrotate and database retention checks. | This is unscoped read-only host discovery. |
| Check journald disk use | `journalctl --disk-usage` is used in the unbounded log scenario. | This is unscoped read-only journal metadata. |
| Check kernel/OOM evidence | `journalctl -k --since ...` is used for OOM and kernel-kill investigation. | This is unscoped read-only kernel journal access. |
| Vacuum journald | `journalctl --vacuum-size=500M` is used for disk recovery. | This is unscoped mutation and needs explicit opt-in plus remediation mode. |

## Agreed Policy Model

The design should add a system-service policy boundary, but keep the config
minimal.

`AllowedCommands` remains the first gate. If `rshell:systemctl` or
`rshell:journalctl` is not allowed, none of the corresponding command behavior
is reachable.

The new service config should use generic "system service" names rather than
public API names tied to systemd. The v1 implementation can still be explicitly
Linux/systemd-backed.

Proposed API shape:

```go
interp.AllowedCommands([]string{
	"rshell:systemctl",
	"rshell:journalctl",
})

interp.AllowedSystemServiceRead([]string{
	"mysql.service",
	"cron.service",
	"logrotate.timer",
})

interp.AllowedSystemServiceControl([]interp.SystemServiceControlGrant{
	{
		Service: "mysql.service",
		Verbs: []interp.SystemServiceVerb{
			interp.SystemServiceRestart,
			interp.SystemServiceReload,
		},
	},
})

interp.AllowSystemJournalVacuum()
interp.WithMode(interp.ModeRemediation)
```

Suggested public types:

```go
type SystemServiceVerb string

const (
	SystemServiceStart   SystemServiceVerb = "start"
	SystemServiceStop    SystemServiceVerb = "stop"
	SystemServiceRestart SystemServiceVerb = "restart"
	SystemServiceReload  SystemServiceVerb = "reload"
)

type SystemServiceControlGrant struct {
	Service string
	Verbs   []SystemServiceVerb
}
```

## Authorization Semantics

| Operation | Required config |
| --- | --- |
| `systemctl --state=failed` | `AllowedCommands("rshell:systemctl")` |
| `systemctl list-timers` | `AllowedCommands("rshell:systemctl")` |
| `systemctl status UNIT` | `AllowedCommands("rshell:systemctl")` plus `AllowedSystemServiceRead(UNIT)` or a control grant for `UNIT` |
| `systemctl show UNIT --property=NRestarts` | `AllowedCommands("rshell:systemctl")` plus `AllowedSystemServiceRead(UNIT)` or a control grant for `UNIT` |
| `systemctl start/stop/restart/reload UNIT` | `AllowedCommands("rshell:systemctl")` plus `AllowedSystemServiceControl` for the exact verb and `UNIT`, plus remediation mode |
| `journalctl --disk-usage` | `AllowedCommands("rshell:journalctl")` |
| `journalctl -k --since TIME` | `AllowedCommands("rshell:journalctl")`; output must be bounded |
| `journalctl -u UNIT -n N --no-pager` | `AllowedCommands("rshell:journalctl")` plus `AllowedSystemServiceRead(UNIT)` or a control grant for `UNIT` |
| `journalctl -u UNIT --since TIME` | `AllowedCommands("rshell:journalctl")` plus `AllowedSystemServiceRead(UNIT)` or a control grant for `UNIT`; output must be bounded |
| `journalctl --vacuum-size=SIZE` | `AllowedCommands("rshell:journalctl")` plus `AllowSystemJournalVacuum()` plus remediation mode |

Control grants imply read access for the same service. This supports pre-checks
and post-checks without forcing callers to duplicate service names in both read
and control config.

## Minimal Config Decision

Do not add separate config for narrow unscoped read-only discovery in v1.

Once an operator explicitly allows `rshell:systemctl`, the command may expose
only the narrow unscoped read-only surfaces needed by scenarios:

- failed unit discovery
- timer discovery

Once an operator explicitly allows `rshell:journalctl`, the command may expose
only the narrow unscoped read-only journal surfaces needed by scenarios:

- journal disk usage
- bounded kernel journal reads

This mirrors existing rshell precedent: commands such as `df`, `ss`, `ip route`,
and `ps` expose bounded host metadata once the command itself is allowed. They
do not require an additional "host observability" config.

Do require explicit config for unscoped mutation. `journalctl --vacuum-size=SIZE`
deletes host-wide historical journal data, so command allowlisting and
remediation mode are not enough on their own.

## Service Identity Rules

Configured service identities should be exact and canonical. Do not support
globs, regexes, broad prefixes, or script-controlled matching.

The command parser may apply limited systemctl-compatible normalization before
policy checks. For example, a bare `mysql` command operand can normalize to
`mysql.service`, while explicit unit names such as `logrotate.timer` remain
exact.

Examples:

```go
interp.AllowedSystemServiceRead([]string{"mysql.service"})
```

can authorize:

```sh
systemctl status mysql
systemctl status mysql.service
journalctl -u mysql -n 50 --no-pager
journalctl -u mysql.service -n 50 --no-pager
```

but it should not authorize arbitrary aliases, globs, or unrelated units.

## Deferred Scope

Do not include full `systemctl` or `journalctl` compatibility in v1.

Deferred `systemctl` examples:

- `mask`
- `unmask`
- `enable`
- `disable`
- `edit`
- `daemon-reload`
- `daemon-reexec`
- arbitrary `show`
- arbitrary `list-units`
- job management
- environment changes
- unit file writes
- user-manager or remote-machine modes

Deferred `journalctl` examples:

- live follow
- cursor/export modes
- JSON and other output-format variants
- boot selection
- catalog output
- arbitrary field queries
- unscoped whole-journal reads such as `journalctl --since TIME`
- `--directory`
- `--file`
- `--root`
- `--image`
- remote journal sources
- `--vacuum-time`
- `--vacuum-files`

`daemon-reload` and `daemon-reexec` are intentionally deferred even though
scenarios mention them. They are unscoped systemd manager mutations and mostly
make sense after unit/config-file edits that rshell is not modeling safely in
this v1 policy.

## Implementation Recommendation

The research recommendation is to avoid invoking host `systemctl` or
`journalctl` binaries as raw wrappers. Use native/internal systemd and journal
access paths or tightly scoped internal adapters.

Rationale:

- The command grammar should be the supported rshell subset, not the full host
  binary surface.
- Argument validation, output bounds, and context cancellation are easier to
  enforce internally.
- Wrapper behavior would depend on host systemd/journalctl versions.
- A raw wrapper risks accidentally accepting flags outside the intended safety
  model.

This remains an implementation recommendation rather than a separately agreed
policy decision.

## Summary

The minimal viable config addition is:

- `AllowedSystemServiceRead([]string{...})`
- `AllowedSystemServiceControl([]SystemServiceControlGrant{...})`
- `AllowSystemJournalVacuum()`

Everything else should continue to flow through existing gates:

- `AllowedCommands` gates command availability.
- `AllowedPaths` gates user-supplied filesystem paths.
- `ModeRemediation` gates mutations.

This keeps service control explicit, preserves a small API surface, supports the
scenario-backed unscoped read-only discovery commands, and avoids granting
unscoped destructive journal cleanup without deliberate opt-in.
