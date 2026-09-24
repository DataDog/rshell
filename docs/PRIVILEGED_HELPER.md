# Privileged helper

The `rshell privileged-helper` mode is the Linux-only, socket-activated
execution boundary for selectively elevated Private Action Runner tasks. It is
not enabled by the normal rshell CLI.

The helper must start with real UID 0. It loads its trust policy, resolves the
`dd-agent` UID and complete group set, permanently switches its real, effective,
and saved GIDs to the account's primary GID, installs only that account's
supplementary groups, and drops its effective UID to `dd-agent`. The
socket-facing process verifies requests but never interprets a command itself.
For every verified request it starts a fresh copy of the same binary in hidden
`privileged-worker` mode and sends only the verified effective command policy
over a bounded, length-delimited stdin protocol. The one-shot worker applies
its command-specific sandbox, executes exactly one request, and exits. Context
cancellation kills the worker.

Only commands explicitly prefixed with `sudo` and present in the effective
`elevatableCommands` policy may temporarily restore effective UID 0. When a
local policy is configured, the effective policy is the intersection of that
policy and the signed task; otherwise it is the signed task policy. The worker
deliberately retains real UID 0 so Linux
`setresuid(2)` can implement that narrow callback. Landlock and seccomp are
installed before interpretation and remain active across the temporary
effective-UID change.

In `runRemediationCommand`, a write-target redirect (`>`, `>|`, `>>`, `2>`,
`2>|`, `2>>`, `&>`, `&>>`) attached to a `sudo <elevatable-command> ...`
statement (a literal or dynamically expanded "sudo" marker; see below)
opens its already-expanded target path elevated, so e.g.
`sudo echo data > /root-only/file` can create or write a
target that only root's DAC permissions allow, as long as the target is
still within a `:rw` `AllowedPaths` root. Landlock and `AllowedPaths`
containment are enforced independently of effective UID and remain unchanged
by this: elevation only lets the interpreter's own DAC-level checks and open
succeed, it never widens the kernel sandbox.

Elevation covers the redirect's non-regular-file type-check together with the
sandboxed open it guards, run as a single privileged unit — not merely one
`open(2)` call. The sandboxed open itself performs the no-follow directory
traversal, the underlying `open(2)`, a link-count check on the resulting
descriptor, and (for `>`/`>|`/`&>`, which truncate) a following `ftruncate`, all
at elevated effective UID, exactly as the same sequence runs unprivileged for
an ordinary (non-`sudo`) redirect. Expanding the redirect word itself (which
can run a command substitution, e.g. `> "$(cmd)"`) always runs at the
caller's ordinary privilege beforehand, never elevated, regardless of
whether the substituted command is itself authorized to elevate. Because
this elevation decision is made after the command word is already fully
expanded (the same point at which `call()` itself decides whether to
elevate the command), a dynamically expanded "sudo" marker (e.g.
`m=sudo; $m echo data > /root-only/file`) elevates its own redirect exactly
like a literal one — there is no separate static-literal restriction on
this path, unlike the read-only-mode static check for a non-`/dev/null`
target. A redirect is only ever elevated for a command
name that is (a) in the effective `AllowedCommands` (or covered by
`AllowAllCommands`), (b) in the elevatable-commands policy, and (c) a
registered builtin — the same three-gate requirement `call()` itself
enforces, in the same order, before dispatching an elevated command — so a
redirect on a non-elevatable, disallowed, unregistered, or otherwise
elevatable-but-not-runnable `sudo` command is never opened elevated, and a
rejected elevation cannot leave a root-owned file behind as a side effect.

A nested command in a later word of the *same* statement — in particular a
command substitution such as `sudo echo "$(cmd)" 2>/root-only/out` — cannot
reach the elevated redirect target merely by inheriting the file descriptor,
which a Unix `write(2)` would otherwise honor regardless of the calling
code's current privilege. The elevated write-target file is wrapped so that
a nested runner created for that command substitution (or a pipeline stage,
or an explicit subshell) receives the pre-elevation stream instead of the
elevated one, so `cmd` cannot write into the root-only target despite never
being authorized to elevate. This holds even across multiple elevated
redirects stacked on the same statement (e.g. two `2>` redirects, which nest
fallback wrappers rather than a single one), and the `$(<file)` command-
substitution shortcut — which runs without creating a nested runner at all,
since it never executes a command — prints its own diagnostics to the
pre-elevation stream too, for the same reason.

Interpreter-level diagnostics — a redirect's own setup failure, an argument-
expansion error, or any other message the interpreter (not the authorized
command itself) prints — also never reach the elevated descriptor, even
within the same statement's own dispatch. A statement can carry more than
one redirect (e.g. `sudo true 2>>/allowed/log >"$(expr)"`); once an earlier
redirect has installed the elevated writer, a later redirect's own setup
error could otherwise embed that later redirect's expanded — and
potentially attacker-influenced — target path, including embedded
newlines, into the earlier, unrelated elevated log target merely because
the interpreter's diagnostic channel currently points at it. The elevated
descriptor's content is restricted to exactly what the authorized command
writes through its own output stream, never anything the interpreter
itself prints on the command's behalf.

Rejecting a FIFO, socket, or device as a write target inside that same
elevated window (rather than checking it separately beforehand, at the
worker's ordinary unprivileged UID) is what prevents the worker from hanging
indefinitely on `open(2)` of a root-only FIFO with no reader, since the
sandbox's write path issues a plain blocking open with no `O_NONBLOCK`: an
unprivileged check against a root-only target would fail closed
with "permission denied", which is indistinguishable from "does not exist yet"
and therefore cannot be treated as conclusive, while the subsequent elevated
open could otherwise still reach and hang on the FIFO. If the
selective-elevation callback itself fails — as opposed to the command or
redirect being rejected by ordinary sandbox/policy checks — that failure is
treated as fatal: the whole worker invocation aborts and the underlying error
is returned, the same as the identical failure mode for an elevated command's
own dispatch, rather than leaving the caller with an unexplained bare
failure.

The authenticated action name selects the worker mode. `runCommand` uses
read-only mode and may selectively elevate investigation builtins such as
`cat` or `grep` without enabling write redirections or remediation-only
builtins. `runRemediationCommand` uses remediation mode. Whole-script root
execution is unavailable in both modes.

Signed `system_services` grants become the worker's `AllowedSystemServices`.
They authorize `journalctl` reads in either mode and `systemctl` or journal
cleanup only in remediation mode. `elevatableCommands` remains a separate
requirement when an operation needs effective UID 0.

## Authorization layers

The effective policy for a privileged request is the intersection of up to
three independent layers, applied in this order: the signed backend task,
the unsigned Agent-supplied policy carried on the request (see
"Agent-supplied policy" below), and the optional local root-owned
`policy.json` (see "Optional local policy" below). Any layer that is absent
imposes no narrowing; a present layer only ever narrows what the preceding
layers already allow.

## Agent-supplied policy

Every `ExecuteRequest` may carry an unsigned `agentPolicy` object alongside
the signed envelope. This is how the Datadog Agent's Private Action Runner
forwards its own `private_action_runner.restricted_shell.allowed_commands`,
`allowed_paths`, and `allowed_system_services` `datadog.yaml` settings (plus
an elevatable-commands equivalent) to the privileged path — the same
operator settings that already narrow the non-privileged rshell path.

Because `agentPolicy` is unsigned and supplied by a process the helper does
not fully trust, it can only narrow the signed backend policy and any local
`policy.json` — it can never grant a permission beyond what those already
allow. A request with no `agentPolicy` at all behaves exactly as if this
field never existed: the effective policy is signed task ∩ optional
`policy.json`.

When `agentPolicy` is present, each of its four fields
(`allowedCommands`, `allowedPaths`, `allowedSystemServices`,
`elevatableCommands`) is applied independently, using the same nil-vs-empty
convention as `policy.json` and the Agent's own non-privileged operator
settings: a field that is entirely omitted (nil) leaves that axis
unrestricted by this layer, deferring to the signed task and `policy.json`
unchanged; a field that is explicitly present but empty is a kill switch that
denies every grant on that axis. This lets an operator configure only some
`datadog.yaml` settings — for example only `allowed_commands` — without
accidentally denying every system service or path just because those
settings were left unset.

## Optional local policy

The systemd unit optionally loads `/etc/datadog-agent-rshell/policy.json`
before dropping the helper's effective UID. If the file does not exist, the
helper starts without a local policy. The separate administrator-controlled
privileged-rshell opt-in remains the gate for enabling the socket and is not
represented by this file.

Without a local policy, the effective policy is the signed backend task
intersected with any Agent-supplied policy on the request; the helper
authenticates the original task envelope with the bare public key supplied by
the Agent, which the Agent supplies only after verifying it through the
Director metadata flow.

When present, the file is a root-owned authorization policy that further
narrows those values, beneath both the signed task and any Agent-supplied
policy. Its minimal form is:

```json
{
  "version": 1,
  "allowedCommands": ["rshell:*"],
  "allowedPaths": ["/var/log:rw"],
  "allowedSystemServices": {
    "mysql.service": ["read", "restart"],
    "systemd-journald.service": ["read"]
  },
  "elevatableCommands": ["rshell:journalctl", "rshell:systemctl", "rshell:truncate"]
}
```

Service names and actions are intersected exactly; `*` retains every action
granted by the other policy. Omitting `allowedSystemServices` from a local
policy denies every systemd action, matching the analogous convention for
`agentPolicy` described above. The Agent's ordinary
`private_action_runner.restricted_shell.allowed_system_services` setting (and
its `allowed_commands`/`allowed_paths` counterparts) now applies to privileged
requests too, via the Agent-supplied policy layer. `policy.json` remains
available as an additional, root-owned layer that only ever narrows further
beneath signed ∩ agent policy — useful when local restrictions must not be
expressible from, or overridable by, the Agent's own configuration.

The file must be written atomically by a root-owned installer or configuration
path and must not be group- or world-writable. For compatibility, it may also
contain `orgId` and `runnerId` together, static `keys`, or `directorRoot` trust
material. A configured `directorRoot` makes the helper independently validate
the Director proof instead of using the Agent-verified bare key. These fields
are optional; they are not credentials required for the normal policy-only or
no-policy modes.

The helper verifies the backend signature over the original protobuf bytes,
task expiration, organization, runner identity, action name, signed backend
allowlists, `effectivePermissions: EscalationAllowed`, and the signed
`elevatableCommands` list. A missing or malformed field fails closed.

Socket callers dispatch the original signed envelope with
`Client.ExecuteSignedTask`. The command, effective permissions, and elevatable
command list are never copied into the outer socket request: doing so would
create an unsigned second source of authorization policy. The helper decodes
those fields into typed inputs only after authenticating the envelope.

The Agent includes both the verified bare public key and the ordered Director
root updates, signed Targets metadata, the selected `AP_RUNNER_KEYS` target
path, and the raw target bytes in the outer socket request. The Agent validates
root rotation, Targets signatures and expiration, the target hash, the
per-organization `AP_RUNNER_KEYS` path, and the target's public-key encoding.
When a local `directorRoot` is configured, the helper independently repeats
those checks and ignores the bare key. The protocol-v1 wire representation
carries both forms in the existing `verificationKeys` slot; the Director proof
has type `TUF_DIRECTOR`.

## Authorization diagnostics

The helper writes one-line JSON diagnostics to standard error, which systemd
records in the service journal. Successful verification logs the task,
organization, runner, action, expiration, effective-permissions value, trusted
key count, and the signed, Agent-supplied, local, and effective command,
path, system-service, and elevation policies. A request with no
Agent-supplied policy logs an all-empty `agent` policy, matching how an
absent `policy.json` logs an all-empty `local` policy. Verification failures
log the failure, non-secret key metadata, the request's Agent-supplied
policy, and the configured local policy. Execution completion logs only the
task ID and exit code.

Diagnostics deliberately exclude command text, signatures, public-key PEM
contents, stdout, and stderr. Those values are unnecessary for policy
intersection debugging and can contain sensitive data. Inspect the records
with:

```sh
journalctl -u datadog-agent-rshell-privileged.service
```

Scripts containing elevated commands currently reject all pipelines because
rshell executes pipeline stages concurrently while effective UID is
process-wide. The helper's static precheck treats any command word that is not
a plain literal (quoting, escapes, expansions, globs) as a possible `sudo`
marker. The interpreter also refuses `sudo` at dispatch — and, in remediation
mode, refuses to elevate that statement's own write-target redirect open —
anywhere inside a pipeline stage, including nested `(…)` subshells and
command substitutions, so indirection such as `m=sudo; ($m cat f) | grep x`
cannot elevate a stage. Whole-script root mode is intentionally unsupported.

The helper binary must be built with `CGO_ENABLED=0`. Linux credentials are
per-thread, and Go cannot apply its all-runtime-thread credential syscall when
cgo may have created threads outside the runtime. A cgo-enabled helper fails
closed during its initial privilege drop; the Datadog Agent packaging task
enforces the pure-Go build.

## One-shot worker sandbox

The worker derives its Landlock rules directly from the effective
`allowedPaths` already computed from the authenticated backend task and the
optional local policy. There is no second backend policy type and no unsigned
path input. The worker inherits the helper's unprivileged effective UID, parses
the authenticated script, and then temporarily restores effective UID 0 only
for trusted initialization. During that bounded window it opens the verified
roots, installs Landlock and seccomp, and constructs the interpreter's
`os.Root` handles. It drops back to `dd-agent` before evaluating any script.
This permits an elevated read of a root-only path without giving an ordinary
command access to it. A missing root, Landlock ABI below 3, unsupported
architecture, or any sandbox installation error fails the request before the
interpreter is created.

Landlock handles every filesystem right available through ABI 3. Unsuffixed
and `:ro` roots grant file reads and directory listing. `:rw` additionally
grants regular-file creation, writes, and truncation only for
`runRemediationCommand`; `runCommand` downgrades every path to read-only before
creating the kernel ruleset. The ordinary remediation runner supports the `rm`
builtin, but the privileged worker does not grant Landlock file or directory
deletion rights, so `rm` is unavailable through this elevated path. It also
does not grant directory creation, rename/link, execution,
symlink/FIFO/socket/device creation, or other special-file mutation. A
read-only child below a read-write root is rejected in remediation mode because
additive Landlock rules cannot represent that override without widening it.
Each root is opened once with `O_PATH`, and the same descriptor is used for
validation and rule creation. `/dev/null` is always granted exact-file
read/write access to preserve rshell redirection semantics.

Some registered Go builtins intentionally read fixed kernel pseudo-files
outside `AllowedPaths`. The worker adds these read-only rules only when the
corresponding command is in the verified effective command allowlist:

| Allowed command | Additional Landlock access |
|-----------------|----------------------------|
| `rshell:ps` | `/proc` hierarchy |
| `rshell:ss`, `rshell:ip` | `/proc/net` hierarchy |
| `rshell:df` | exact file `/proc/self/mountinfo` |
| `rshell:uname` | exact files `/proc/sys/kernel/{ostype,hostname,osrelease,version,arch}` |

The complete fixed set is granted for an allowed builtin because shell
expansion can choose its flags at runtime. Systemd-aware builtins similarly use
the fixed local target (`/etc/machine-id`, the standard journal directories,
the journald control socket, and the public system D-Bus socket), but their
Landlock exceptions are derived from both the allowed command and the effective
service actions:

| Effective capability | Additional Landlock access |
|----------------------|----------------------------|
| `rshell:journalctl` plus any exact `read` grant | exact file `/etc/machine-id` and read-only access to the standard journal directories |
| remediation-mode `rshell:journalctl` plus `systemd-journald.service:clean` | exact file `/etc/machine-id` and read/remove-file access to the standard journal directories |
| remediation-mode `rshell:systemctl` plus any manager action | exact file `/etc/machine-id` |

The journal directories are optional. Only an effective journald `clean`
capability receives file-removal access, and the journal backend still limits
deletion to validated archived files. Landlock ABI 3 does not mediate Unix
sockets, so fixed paths, descriptor pinning, machine-ID validation, and the
service-action policy protect the control endpoints. The helper supports only
the local systemd target.

After Landlock, the worker installs a TSYNC seccomp filter with default-allow
semantics and `EPERM` for the reviewed denylist. It blocks:

- process creation or image replacement (`fork`, `vfork`, `clone3`, `execve`,
  `execveat`), with `clone` allowed only for the exact Go runtime thread flags;
- credential/capability changes other than the required `setresuid` callback;
- namespaces, root changes, classic mounts, and the new mount API;
- BPF, perf, ptrace, cross-process memory and pidfd access, keyrings, kernel
  modules, kexec, and reboot;
- device nodes, `ioctl`, ownership/mode/xattr/timestamp mutation, system-clock
  changes, process signaling/scheduling changes, io_uring, userfaultfd,
  file-handle opens, fanotify, raw I/O, swap, accounting, and quota control.

The denylist is centralized in `internal/sandbox/seccomp/seccomp.go`. Seccomp
also sets `no_new_privs`; Landlock must be installed first because the final
filter denies `prctl`. Both policies are synchronized to all Go runtime threads.
