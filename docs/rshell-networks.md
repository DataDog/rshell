# RFC: Network Investigation and Remediation with rshell

| Field | Value |
|---|---|
| Status | Draft |
| Scope | Datadog Agent and rshell execution layer |
| Audience | rshell, Datadog Agent, and Network Device Monitoring maintainers |

## Summary

This RFC proposes a safe way for AI agents to investigate and remediate network devices from a Datadog Agent with rshell. The first version exposes a small set of reviewed, capability-oriented device operations while keeping target resolution, credentials, connectivity, protocols, and vendor translation inside the Datadog Agent.

A requirement of the network solution is a general rshell extension framework through which the Datadog Agent registers typed command trees. That framework requires a separate RFC and is a dependency of this proposal. This RFC defines the requirements network operations place on the framework, but does not define its API or general authorization model. Agent Health is a second candidate that demonstrates why this boundary must be reusable, but Agent Health itself is outside the scope of this RFC.

The proposal does not expose arbitrary native device commands. Instead, reviewed command leaves such as `device interfaces show` and `device interface set-state` map to typed Agent handlers. This preserves rshell's restricted execution model while giving AI agents composable capabilities for investigation and remediation.

## Problem Statement

Datadog AI agents can detect and reason about network incidents using telemetry, topology, and configuration history, but they lack a secure way to actively investigate and remediate the underlying network from within a customer's private environment.

Existing capabilities address parts of this problem: the Datadog Agent provides a customer-side execution environment, rshell safely executes constrained operations, and Network Configuration Management (NCM) communicates with network devices and supports configuration collection and rollback. The Private Action Runner is an existing Datadog Agent mechanism for receiving and executing actions from Datadog within a customer's private network. However, there is no general execution interface that lets an AI agent perform open-ended host diagnostics, invoke reviewed vendor-aware device operations, coordinate operations across multiple network devices, and verify remediation outcomes.

Without this capability, investigations must fall back to predefined actions or human operators with direct device access. This limits an AI agent's ability to diagnose dynamic operational state, correlate observations across devices, respond to the long tail of vendor-specific failures, and complete remediation workflows.

The challenge is to enable bounded, auditable, protocol-agnostic network investigation and remediation from a Datadog Agent while preserving the customer's security boundary. Operations must support heterogeneous devices and multi-device workflows, keep credentials hidden from AI agents, remain constrained by backend policy and independent local safeguards, and execute as isolated, time-bounded actions rather than unrestricted interactive access.

## Goals

- Let the Datadog Agent expose reviewed investigation and remediation capabilities as rshell commands.
- Preserve rshell's default-deny model and local defense-in-depth controls.
- Support natural command trees with precise authorization of executable leaves.
- Keep credentials, connections, protocols, and vendor-specific behavior inside the Datadog Agent.
- Provide stable, composable output across device vendors.
- Support workflows spanning multiple devices through rshell composition or backend orchestration.
- Use a general extension mechanism rather than introducing a network-specific embedding API in rshell.

## Non-Goals

- Designing backend authorization, approval, or AI orchestration policy.
- Selecting or implementing the final device credential provider.
- Giving AI agents access to device credentials.
- Providing arbitrary native device command execution.
- Reproducing complete vendor CLI grammars in rshell.
- Providing persistent or interactive device sessions.
- Supporting multi-target execution within one extension command.
- Implementing Agent Health commands as part of this RFC.
- Designing or implementing the general rshell extension framework.
- Defining a complete network-device command catalog.

## Background

### rshell

rshell is a restricted shell interpreter for AI agents. It provides a default-deny command allowlist, a filesystem sandbox, bounded execution, an empty environment by default, and separate read-only and remediation modes. Its builtins own their parsing and validation, and external command execution is blocked unless the embedding application explicitly provides a handler.

Today, `AllowedCommands` authorizes top-level command identities such as `rshell:cat`, `rshell:ping`, and `rshell:ip`. Some builtins have subcommands, but authorization does not distinguish between those subcommands.

### Datadog Agent and Private Action Runner

The Datadog Agent is the customer-side environment in which the proposed capabilities execute. The Private Action Runner is one existing mechanism for delivering authorized actions from Datadog to an Agent in a private network. This RFC does not require the Private Action Runner to be the only future delivery mechanism.

### Network Configuration Management

NCM already provides device registration, SSH connectivity, credential use, host verification, profile matching, per-device locking, configuration retrieval, configuration storage, and configuration rollback. Its current remote interface is oriented around configuration collection and push operations rather than a general catalog of typed troubleshooting capabilities.

NCM is a possible source of device identities, connections, and credentials for the proposed design. Backend-provided connection information is another possibility. The extension interface must not depend on either source.

### Requirement for a general extension mechanism

The network solution requires the Datadog Agent to expose Agent-owned operations without moving target resolution, credentials, protocols, or vendor behavior into rshell. This boundary must be general rather than network-specific and will be proposed in a separate RFC. Agent Health is another likely consumer: it may need to expose Agent-owned diagnostics and remediation through rshell and would need the same registration, validation, authorization, mode, help, telemetry, and execution contracts.

Agent Health provides evidence for the generality requirement, not a second subject for this RFC. The proposed design and v1 validation remain centered on network investigation and remediation.

## Use Cases and Requirements

### Investigation

AI agents should be able to inspect operational state such as:

- Interface status, addresses, errors, and descriptions.
- Routing tables and selected routes.
- Routing-protocol neighbor state.
- Reachability from the Agent host and from supported devices.
- Relevant differences between observed state and known configuration.

### Remediation

AI agents should be able to invoke reviewed state-changing capabilities such as:

- Enabling or disabling an interface.
- Restoring a known-good configuration through an existing NCM operation.
- Applying future narrowly scoped, typed remediation operations.
- Verifying device state after a remediation.

### Multi-device workflows

Each extension command operates on exactly one target. Workflows spanning multiple devices are composed by the backend or through rshell control flow:

```sh
for target in edge-router-1 edge-router-2; do
    device interfaces show "$target"
done
```

This keeps connection handling, exit status, retries, and audit records scoped to a single device operation. Concurrent orchestration remains a backend responsibility for the first version.

### Execution requirements

- Commands are non-interactive and bounded by context cancellation and timeouts.
- Targets are explicit; commands cannot discover and operate on an unrestricted device fleet.
- Arguments are parsed and validated before an Agent handler is invoked.
- Investigation and remediation operations are classified separately.
- Credentials never enter the rshell program, output, or AI-agent context.
- Output and errors have deterministic contracts suitable for automation.

### Extension requirement

The implementation depends on a general rshell extension framework rather than a network-specific `DeviceExecutor` option. A separate RFC must define that framework. To support this proposal, the framework must provide:

- Typed command trees with independently authorizable executable operations.
- Read-only and remediation classification enforced before handler dispatch.
- Argument validation and generated help.
- Bounded, cancellable Agent handlers.
- Deterministic text and JSON output contracts.
- Safe registration that grants no implicit filesystem, environment, process, or credential access.

Network-device commands are the first implementation delivered by this RFC. The companion framework RFC owns the general API, command-identity encoding, allowlist syntax, wildcard semantics, compatibility, and behavior for other extension domains.

### Design insights for the companion RFC

The discussion behind this RFC produced several framework-level design insights. They are retained here as recommendations and input to the companion RFC, not as framework-wide decisions made by this RFC.

#### Prefer one command policy model

The framework should extend the existing `AllowedCommands` concept instead of adding a separate `AllowedCapabilities` policy. A registered executable operation should resolve to one canonical rshell command identity and pass through the same backend/operator policy intersection as builtins.

Existing builtins can remain one-segment executable paths:

```text
rshell:cat
rshell:ip
```

Extension operations can use deeper executable paths:

```sh
device interfaces show edge-router-1
```

with a canonical identity such as:

```text
rshell:device.interfaces.show
```

Under this model, `rshell:cat` and `rshell:device.interfaces.show` follow the same rule: each identifies a registered executable path. Intermediate namespace nodes such as `rshell:device` or `rshell:device.interfaces` are not exact grants unless they are themselves registered as executable operations.

#### Authorize executable leaves precisely

Each executable leaf should have an independent policy and audit identity. Adding a future remediation operation must not silently expand an exact grant for an existing investigation operation.

The companion RFC should consider a deliberately limited terminal namespace wildcard for operator convenience:

```text
rshell:device.interfaces.*
```

Arbitrary patterns such as `device.*.show` or partial-name globbing should not be supported. Backend tasks should authorize exact leaves even when local operator configuration uses a terminal wildcard. The effective policy remains the intersection of both sides, so an operator wildcard cannot authorize an operation by itself.

#### Register typed command trees

Extensions should declare command paths, descriptions, typed arguments, execution modes, and handlers. The framework should reject duplicate or ambiguous paths, incomplete registrations, invalid identities, and missing required metadata during runner construction.

Command resolution should happen before authorization and dispatch. The framework should resolve the complete executable path, validate its arguments, check its mode and effective command policy, and only then invoke the Agent handler.

#### Keep handlers narrow and bounded

Handlers should receive typed inputs and a cancellable context rather than unrestricted access to shell internals. Registration must not implicitly grant filesystem, environment, process-execution, credential, or network access. Any privileged capability must be supplied deliberately by the embedding application.

The framework should provide consistent help, telemetry, timeout propagation, output bounds, panic containment, and error mapping for all extension domains.

#### Validate the abstraction with another domain

Agent Health should be used as a design test before the general API is declared stable. The framework should be able to represent both network-device operations and Agent-owned health diagnostics without embedding domain-specific types in rshell. Implementing Agent Health remains outside this RFC.

## Proposed Network Architecture

### Overview

Assuming the companion extension framework, the Datadog Agent registers a network-device command tree with rshell. Each executable operation provides network-specific metadata, arguments, an execution-mode requirement, and an Agent handler.

For a device operation, execution proceeds as follows:

1. rshell parses the shell program and identifies the top-level extension command.
2. The extension framework resolves the requested executable operation.
3. rshell verifies that the operation is enabled by the effective command policy.
4. rshell validates arguments and verifies that the current execution mode permits the operation.
5. rshell invokes the registered Agent handler with a bounded context and typed inputs.
6. The Agent resolves the opaque target through a device provider.
7. The provider obtains an authenticated connection without exposing credentials to rshell.
8. A vendor or protocol adapter performs the typed operation and returns a structured result.
9. rshell renders the result and records the exit status and telemetry.

### Responsibility boundary

The companion rshell extension framework owns:

- Command-tree registration and resolution.
- Command identity and command-policy enforcement.
- Argument-schema validation.
- Read-only and remediation-mode enforcement.
- Help and usage generation.
- Context cancellation, timeout propagation, and output bounds.
- Stable text and JSON rendering contracts.
- Framework-level telemetry and errors.

The Datadog Agent owns:

- Extension handlers and domain behavior.
- Opaque target resolution.
- Credential acquisition and isolation.
- Device connection lifecycle and locking.
- Protocol selection and vendor translation.
- Parsing native responses into structured results.
- Domain-level telemetry and audit context.

### Network command registration

The companion RFC defines the registration API. The network implementation needs to register operations equivalent to the following illustrative definition:

```go
ExtensionCommand{
    Path:        []string{"device", "interfaces", "show"},
    Description: "Show network interfaces on a device.",
    Mode:        ModeReadOnly,
    Arguments:   []Argument{{Name: "target", Type: String, Required: true}},
    Handler:     showInterfaces,
}
```

Handlers receive only their typed inputs, standard output contract, and a cancellable context. Registering an extension must not implicitly grant access to the host filesystem, process execution, the host environment, credentials, or other rshell internals.

Registration failures are handled according to the companion framework RFC and must prevent unsafe or ambiguous network operations from becoming available.

## Network Command and Authorization Requirements

### Executable command paths

Network operations use natural command paths:

```sh
device interfaces show edge-router-1
```

Each executable network operation must have a distinct policy and audit identity. Allowing an investigation operation must not implicitly allow a remediation operation, and adding a future operation must not silently expand an existing exact grant. The companion framework RFC decides how those identities are encoded in `AllowedCommands`, whether namespace wildcards exist, and how backend and operator policy are expressed and intersected.

### Execution modes

Every executable network operation declares its minimum mode:

- Investigation leaves run in read-only and remediation modes.
- Remediation leaves run only in remediation mode.

Mode classification is mandatory registration metadata and is enforced by the extension framework before dispatch. A network handler cannot downgrade or bypass this check.

## Device Provider and Adapter Boundary

The Agent-side device provider accepts an opaque target reference and returns access to typed device capabilities. It is responsible for resolving the device, locating credentials, applying connection policy, selecting a protocol or vendor adapter, and closing the connection.

The provider must support different credential sources without changing rshell commands. Candidate implementations include:

- Reusing NCM-registered devices and credentials.
- Resolving backend-provided connection information inside the Agent.
- Supporting a future Agent credential provider.

The AI agent and rshell program operate only on opaque target references. Raw usernames, passwords, private keys, and tokens are not valid command arguments or handler results.

Vendor adapters translate typed operations into the appropriate native protocol operations and normalize responses. SSH is a likely first transport, but the interface must permit NETCONF, REST APIs, controllers, or other protocols.

## V1 Device Command Catalog

The first version implements a small vertical slice rather than a complete network automation surface.

### Investigation commands

```text
device interfaces show TARGET
device routes show TARGET
device routing-neighbors show TARGET
```

These commands validate typed reads, normalized cross-vendor results, command-tree authorization, and the Agent provider boundary.

### Remediation command

```text
device interface set-state TARGET INTERFACE up|down
```

This command validates remediation-mode enforcement, typed state changes, argument validation, and post-operation error reporting. Configuration rollback may continue to use NCM's existing dedicated action in v1.

The final names and detailed schemas may change during implementation, but the v1 catalog must retain equivalent coverage of read-only and state-changing operations.

## Output and Error Contract

Agent handlers return structured domain results. rshell renders those results rather than passing through raw native device output.

The default format is deterministic, native-inspired text. For example:

```text
INTERFACE    STATUS  PROTOCOL  ADDRESS         DESCRIPTION
Ethernet1    up      up        10.0.0.1/30     uplink-core
Ethernet2    down    down      -               unused
```

Commands also support a stable JSON representation for machine consumption:

```sh
device interfaces show --output json edge-router-1
```

Raw native output is not exposed in v1 because it is vendor-dependent, difficult to validate, and may contain unexpected or sensitive content.

The output contract follows normal shell conventions:

- Successful results are written to stdout.
- Diagnostics are written to stderr.
- Exit code `0` means the operation succeeded.
- Exit code `1` means a target or domain operation failed.
- Invocation errors use the standard rshell parsing and usage behavior.
- Framework failures that cannot be represented as normal command failures are returned through the embedding API.

## Security Model

The backend is responsible for deciding which operation should be requested and for applying product-level authorization and approval policy. The Agent and rshell provide an independent enforcement boundary.

Local protections include:

- Intersection of backend and operator `AllowedCommands` policies.
- Exact resolution and authorization of executable command leaves.
- Mandatory investigation or remediation classification.
- Typed argument validation before dispatch.
- Explicit, opaque targets.
- No arbitrary native command payload.
- No credential arguments or results.
- Context cancellation and execution timeouts.
- Bounded output and resource use.
- Agent-owned connection policy and device-side least-privilege credentials.
- Auditable command identity, target identity, mode, outcome, and duration.

Extension handlers are privileged Agent code and require the same security review as builtins. The framework must not present extension registration as a way to execute arbitrary binaries or bypass rshell filesystem, environment, or process restrictions.

## Alternatives Considered

### A dedicated injected `DeviceExecutor` builtin

rshell could implement a fixed `device` builtin backed by a domain-specific `DeviceExecutor` interface. This is simpler for one use case and keeps parsing close to existing builtins. It was not selected because the solution requires a reusable Agent extension boundary; adding one injected interface per Agent domain would not scale cleanly. Agent Health is one known candidate that would otherwise require another domain-specific interface.

### A generic `device exec` command

A generic command could forward arbitrary native CLI text to a target. This offers maximum vendor coverage but turns one allowed rshell command into an unrestricted device-command tunnel. It weakens local defense in depth and makes investigation/remediation classification unreliable. It is explicitly rejected.

### Reimplement native device CLIs as rshell commands

rshell could parse vendor-native commands directly. This would require dynamic vendor grammars, stateful CLI contexts, and significant ongoing compatibility work. It would also create command collisions and couple rshell to network vendors. It is rejected in favor of typed capabilities and Agent adapters.

### Multi-target command handlers

Each command could accept a set of devices and define concurrency and partial-success behavior. This duplicates orchestration already available in rshell and the backend, complicates results and retries, and broadens blast radius. V1 uses single-target operations.

### Exact native output

Returning raw device output would resemble familiar CLI sessions but creates unstable, vendor-specific contracts and may expose unexpected content. V1 uses normalized structured results with native-inspired rendering.

## Compatibility and Migration

The companion framework RFC owns compatibility for existing rshell builtins, command identities, allowlists, and extension registrations. This RFC requires the network implementation to introduce no behavior changes when its commands are not registered or enabled.

The Agent and rshell versions must agree on the extension API version selected by the companion RFC. An Agent without the network implementation exposes no network-device operations.

Help output must list only registered and enabled network operations. Detailed help must not expose or authorize disabled remediation operations.

## Testing and Validation

### Framework dependency tests

The companion RFC owns the general extension-framework test suite. Before the network implementation can depend on it, that suite must demonstrate the registration, authorization, mode enforcement, argument validation, help, cancellation, output bounds, error mapping, and isolation guarantees required by this RFC.

### Agent tests

- Target resolution through each supported provider.
- Credential non-disclosure.
- Connection lifecycle and per-device locking.
- Vendor translation and normalized results.
- Investigation and remediation handlers.
- Unsupported vendor, protocol, and capability behavior.
- End-to-end Private Action Runner execution where applicable.

### Compatibility tests

- Existing help and command behavior remain unchanged when network commands are not registered.
- Network command identities and schemas remain stable across supported Agent upgrades.
- Agent and rshell version mismatches fail safely.

## Rollout and Observability

The proposed rollout is staged:

1. Approve and implement the companion rshell extension-framework RFC.
2. Add the Agent device-provider boundary and read-only v1 commands.
3. Pilot read-only network investigation with a limited set of vendors and protocols.
4. Add the v1 network remediation command behind explicit remediation-mode and command-policy configuration.
5. Expand the network command catalog and adapters based on demonstrated use cases.

Telemetry should record the canonical command identity, execution mode, duration, outcome, provider and adapter type, and a non-secret target identifier. It must not record credentials, native command payloads, or unredacted device output.

The Agent must be able to disable all extensions or individual command leaves through local configuration. Rolling back the Agent or removing a registration removes the corresponding operations without changing rshell's core builtin behavior.

## Dependencies and Open Questions

- The companion rshell extension-framework RFC must be approved and implemented before this proposal can ship.
- Which device and credential provider should be implemented first: NCM, backend-provided connection information, or another Agent provider?
- Which vendors and protocols are required for the initial pilot?
- What stable structured schemas should each v1 device operation return?
- Which telemetry and audit events must be emitted by rshell, the Agent handler, and the backend?
- What evidence is required before remediation capabilities are enabled beyond a limited pilot?
