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

Only commands explicitly prefixed with `sudo` and present in both the signed
task and the local credential's `elevatableCommands` may temporarily restore
effective UID 0. The worker deliberately retains real UID 0 so Linux
`setresuid(2)` can implement that narrow callback. Landlock and seccomp are
installed before interpretation and remain active across the temporary
effective-UID change.

## Verification credential

The systemd unit loads `/etc/datadog-agent/rshell-privileged-helper.json` as a
read-only service credential. This file is a trust root and must be written
atomically by a root-owned installer or key-rotation path. It must never be
accepted from the Unix-socket peer in the production design. The temporary
development path described below deliberately violates that key-provisioning
requirement.

```json
{
  "version": 1,
  "orgId": 1234,
  "runnerId": "private-action-runner-id",
  "keys": [
    {
      "id": "remote-config-key-id",
      "type": "ED25519",
      "pem": "-----BEGIN PUBLIC KEY-----\n...\n-----END PUBLIC KEY-----\n"
    }
  ],
  "allowedCommands": ["rshell:*"],
  "allowedPaths": ["/var/log:rw"],
  "elevatableCommands": ["rshell:truncate"]
}
```

For the temporary socket-key test path, `keys` may be empty. The remaining
credential fields are still required and continue to impose the helper's local
policy ceiling.

The helper verifies the backend signature over the original protobuf bytes,
task expiration, organization, runner identity, action name, signed backend
allowlists, `effectivePermissions: EscalationAllowed`, and the signed
`elevatableCommands` list. A missing or malformed field fails closed.

Socket callers dispatch the original signed envelope with
`Client.ExecuteSignedTask`. The command, effective permissions, and elevatable
command list are never copied into the outer socket request: doing so would
create an unsigned second source of authorization policy. The helper decodes
those fields into typed inputs only after authenticating the envelope.

For development testing only, the Agent may include the public verification key
used for its first verification in the outer socket request. The helper then
uses that key for request-scoped signature verification. This is not a secure
trust bootstrap: a process that can write to the socket can provide its own key
and signature. The temporary path is marked in code and must be removed before
the privileged-helper PR merges. Organization, runner, and local command/path
ceilings still come from the root-owned credential.

## Authorization diagnostics

The helper writes one-line JSON diagnostics to standard error, which systemd
records in the service journal. Successful verification logs the task,
organization, runner, action, expiration, effective-permissions value, trusted
key count, and the signed, local, and effective command/path/elevation policy
lists. Verification failures log the failure and non-secret key metadata plus
the configured local policy. Execution completion logs only the task ID and
exit code.

Diagnostics deliberately exclude command text, signatures, public-key PEM
contents, stdout, and stderr. Those values are unnecessary for policy
intersection debugging and can contain sensitive data. Inspect the records
with:

```sh
journalctl -u datadog-agent-rshell-privileged.service
```

Scripts containing elevated commands currently reject all pipelines because
rshell executes pipeline stages concurrently while effective UID is
process-wide. Whole-script root mode is intentionally unsupported.

The helper binary must be built with `CGO_ENABLED=0`. Linux credentials are
per-thread, and Go cannot apply its all-runtime-thread credential syscall when
cgo may have created threads outside the runtime. A cgo-enabled helper fails
closed during its initial privilege drop; the Datadog Agent packaging task
enforces the pure-Go build.

## One-shot worker sandbox

The worker derives its Landlock rules directly from the effective
`allowedPaths` already computed from the authenticated backend task and the
local credential. There is no second backend policy type and no unsigned path
input. Rules are installed after the helper drops to `dd-agent`, so every
required root must be openable by that account. A missing or inaccessible root,
Landlock ABI below 3, unsupported architecture, or any sandbox installation
error fails the request before the interpreter is created.

Landlock handles every filesystem right available through ABI 3. Unsuffixed
and `:ro` roots grant file reads and directory listing. `:rw` additionally
grants regular-file creation, writes, and truncation; it does not grant file or
directory deletion, directory creation, rename/link, execution, symlink/FIFO/
socket/device creation, or other special-file mutation. A read-only child below
a read-write root is rejected because additive Landlock rules cannot represent
that override without widening it. Each root is opened once with `O_PATH`, and
the same descriptor is used for validation and rule creation. `/dev/null` is
always granted exact-file read/write access to preserve rshell redirection
semantics.

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
expansion can choose its flags at runtime. The privileged-helper protocol does
not currently transport `AllowedSystemServices`, so no trusted journal paths or
deletion rights are added. A future journal integration must derive typed
read/clean rules from authenticated service actions rather than broadening
ordinary `AllowedPaths`.

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
