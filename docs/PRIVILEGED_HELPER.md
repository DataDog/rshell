# Privileged helper

The `rshell privileged-helper` mode is the Linux-only, socket-activated
execution boundary for selectively elevated Private Action Runner tasks. It is
not enabled by the normal rshell CLI.

The helper must start with real UID 0. It loads its trust policy before dropping
its effective UID to `dd-agent`, then accepts one length-delimited request at a
time from the systemd-provided Unix socket. Only commands explicitly prefixed
with `sudo` and present in both the signed task and the local credential's
`elevatableCommands` may temporarily restore effective UID 0.

## Verification credential

The systemd unit loads `/etc/datadog-agent/rshell-privileged-helper.json` as a
read-only service credential. This file is a trust root and must be written
atomically by a root-owned installer or key-rotation path. It must never be
accepted from the Unix-socket peer.

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

The helper verifies the backend signature over the original protobuf bytes,
task expiration, organization, runner identity, action name, signed backend
allowlists, `effectivePermissions: EscalationAllowed`, and the signed
`elevatableCommands` list. A missing or malformed field fails closed.

Scripts containing elevated commands currently reject all pipelines because
rshell executes pipeline stages concurrently while effective UID is
process-wide. Whole-script root mode is intentionally unsupported.

The helper binary must be built with `CGO_ENABLED=0`. Linux credentials are
per-thread, and Go cannot apply its all-runtime-thread credential syscall when
cgo may have created threads outside the runtime. A cgo-enabled helper fails
closed during its initial privilege drop; the Datadog Agent packaging task
enforces the pure-Go build.

Allowed-path roots are opened after the helper drops to `dd-agent`. This avoids
giving ordinary commands directory capabilities acquired as root. Consequently,
the initial implementation can elevate operations on root-owned files beneath
directories traversable by `dd-agent`, but it intentionally cannot reach an
allowed root directory that `dd-agent` cannot open during sandbox construction.
