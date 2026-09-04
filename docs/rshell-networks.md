# RFC: Network Investigation and Remediation with rshell

| Field | Value |
|---|---|
| Status | Draft |
| Scope | Datadog Agent and rshell execution layer |
| Audience | rshell, Datadog Agent, and Network Device Monitoring maintainers |

## Summary

Datadog AI agents can use telemetry, topology, and configuration history to detect and reason about network incidents, but they cannot actively inspect operational state or remediate the affected network devices from within a customer's private environment. As a result, investigations must fall back to narrow predefined actions or human operators with direct device access.

This RFC seeks to enable AI agents to use rshell from a Datadog Agent to investigate heterogeneous network devices, invoke reviewed remediation operations, compose workflows across devices, and verify outcomes. The capability must support the long tail of vendor-specific network behavior without granting unrestricted or interactive device access.

The solution must preserve the customer's security boundary: credentials remain hidden from AI agents, operations are explicit and independently authorized, execution is bounded and auditable, and backend policy is reinforced by local safeguards in the Agent. The rshell implementation mechanism remains a decision for this RFC and is evaluated after the problem, goals, and requirements.

## Problem Statement

Datadog AI agents can detect and reason about network incidents using telemetry, topology, and configuration history, but they lack a secure way to actively investigate and remediate the underlying network from within a customer's private environment.

Existing capabilities address parts of this problem: the Datadog Agent provides a customer-side execution environment, rshell safely executes constrained operations, and Network Configuration Management (NCM) communicates with network devices and supports configuration collection and rollback. The Private Action Runner is an existing Datadog Agent mechanism for receiving and executing actions from Datadog within a customer's private network. However, there is no general execution interface that lets an AI agent perform open-ended host diagnostics, invoke reviewed vendor-aware device operations, coordinate operations across multiple network devices, and verify remediation outcomes.

Without this capability, investigations must fall back to predefined actions or human operators with direct device access. This limits an AI agent's ability to diagnose dynamic operational state, correlate observations across devices, respond to the long tail of vendor-specific failures, and complete remediation workflows.

The challenge is to enable bounded, auditable, protocol-agnostic network investigation and remediation from a Datadog Agent while preserving the customer's security boundary. Operations must support heterogeneous devices and multi-device workflows, keep credentials hidden from AI agents, remain constrained by backend policy and independent local safeguards, and execute as isolated, time-bounded actions rather than unrestricted interactive access.

## Goals

- Let the Datadog Agent expose reviewed investigation and remediation capabilities as rshell commands.
- Preserve rshell's default-deny model and local defense-in-depth controls.
- Provide natural command syntax with precise authorization of individual operations.
- Keep credentials, connections, protocols, and vendor-specific behavior inside the Datadog Agent.
- Provide stable, composable output across device vendors.
- Support workflows spanning multiple devices through rshell composition or backend orchestration.
- Select an rshell integration mechanism based on security, maintainability, implementation cost, and reuse.

## Non-Goals

- Designing backend authorization, approval, or AI orchestration policy.
- Selecting or implementing the final device credential provider.
- Giving AI agents access to device credentials.
- Providing arbitrary native device command execution.
- Reproducing complete vendor CLI grammars in rshell.
- Providing persistent or interactive device sessions.
- Supporting multi-target execution within one device command.
- Implementing Agent Health commands as part of this RFC.
- Defining the detailed API of a general rshell extension framework, if that option is selected.
- Defining a complete network-device command catalog.

## Background

### rshell

rshell is a restricted shell interpreter for AI agents. It provides a default-deny command allowlist, a filesystem sandbox, bounded execution, an empty environment by default, and separate read-only and remediation modes. Its builtins own their parsing and validation, and external command execution is blocked unless the embedding application explicitly provides a handler.

Today, `AllowedCommands` authorizes top-level command identities such as `rshell:cat`, `rshell:ping`, and `rshell:ip`. Some builtins have subcommands, but authorization does not distinguish between those subcommands.

### Datadog Agent and Private Action Runner

The Datadog Agent is the customer-side environment in which the proposed capabilities execute. The Private Action Runner is one existing mechanism for delivering authorized actions from Datadog to an Agent in a private network. This RFC does not require the Private Action Runner to be the only future delivery mechanism.

### Network Configuration Management

NCM already provides device registration, SSH connectivity, credential use, host verification, profile matching, per-device locking, configuration retrieval, configuration storage, and configuration rollback. Its current remote interface is oriented around configuration collection and push operations rather than a general catalog of typed troubleshooting capabilities.

NCM is a possible source of device identities, connections, and credentials for the proposed design. Backend-provided connection information is another possibility. The rshell integration must not depend on either source.

### Agent-owned rshell capabilities

The network solution requires the Datadog Agent to expose Agent-owned operations without moving target resolution, credentials, protocols, or vendor behavior into rshell. Several implementation mechanisms can provide this boundary. This RFC does not assume one before comparing them.

Agent Health is another likely consumer of Agent-owned rshell capabilities. It provides evidence that a reusable mechanism may be valuable, but does not by itself require the network implementation to use a general framework. Implementing Agent Health remains outside this RFC.

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

Each device command operates on exactly one target. Workflows spanning multiple devices are composed by the backend or through rshell control flow:

```sh
for target in edge-router-1 edge-router-2; do
    device interfaces show "$target"
done
```

This keeps connection handling, exit status, retries, and audit records scoped to a single device operation. Concurrent orchestration remains a backend responsibility for the first version.

### Execution requirements

- Commands are non-interactive and bounded by context cancellation and timeouts.
- Targets are explicit; commands cannot discover and operate on an unrestricted device fleet.
- Arguments are parsed and validated before an Agent-owned operation is invoked.
- Investigation and remediation operations are classified separately.
- Credentials never enter the rshell program, output, or AI-agent context.
- Output and errors have deterministic contracts suitable for automation.

### rshell integration requirements

Whichever integration option is selected must provide:

- Independently authorizable network operations.
- Read-only and remediation classification enforced before device execution.
- Argument validation and useful command help.
- Bounded, cancellable Agent-owned operations.
- Deterministic text and JSON output contracts.
- No implicit filesystem, environment, process, network, or credential access beyond capabilities deliberately supplied by the embedding application.
- Stable telemetry, error mapping, and compatibility behavior.

## Decision Required: rshell Integration

Review of this RFC should select one of the following implementation options. The remainder of the RFC describes solution-independent network behavior.

### Option A: General rshell extension framework

rshell provides a reusable mechanism for the Datadog Agent to register typed command trees, metadata, execution modes, and bounded handlers. Network-device commands are its first implementation; Agent Health is a second candidate validating that the abstraction is not network-specific.

Advantages:

- Provides one reusable boundary for Agent-owned rshell capabilities.
- Keeps network and Agent Health implementations in the Datadog Agent.
- Can provide uniform help, authorization, telemetry, cancellation, and output behavior.
- Avoids adding one injected interface to rshell for every Agent domain.

Disadvantages:

- Requires a separate RFC and a larger initial rshell API design.
- Introduces a generic privileged extension surface that needs strong isolation guarantees.
- Increases initial implementation and compatibility work before delivering network operations.

Useful design insights if this option is selected:

- Prefer extending `AllowedCommands` instead of adding a separate `AllowedCapabilities` policy.
- Resolve each executable operation to an independently authorizable identity, such as `rshell:device.interfaces.show`.
- Treat intermediate command namespaces as non-executable unless explicitly registered.
- Consider only terminal namespace wildcards, with backend tasks authorizing exact operations.
- Resolve, authorize, validate, and check execution mode before invoking a handler.
- Give handlers typed inputs and a cancellable context, not unrestricted shell internals.
- Validate the abstraction against Agent Health before declaring the API stable.

### Option B: Injected `DeviceExecutor`

rshell implements a network-specific `device` builtin and accepts an injected, typed `DeviceExecutor` from the embedding Datadog Agent. rshell owns the device command syntax and validation; the Agent implementation owns targets, credentials, connections, protocols, and vendor translation.

Advantages:

- Small, explicit API tailored to the known network operations.
- Keeps security-sensitive parsing and mode checks alongside rshell builtins.
- Requires less generic infrastructure and can deliver a vertical slice sooner.
- Is straightforward to test and audit for the network use case.

Disadvantages:

- Adds network-domain concepts to a general-purpose rshell library.
- Couples rshell and Agent releases when device commands evolve.
- Does not help Agent Health or future Agent-owned domains.
- May lead to one specialized injected interface per domain.

### Option C: Adapt the external-command handler

The Datadog Agent exposes reviewed network operations through an enhanced form of rshell's existing external-command handler. The enhancement supplies the metadata and enforcement needed for allowlisting, help, modes, argument validation, and bounded output.

Advantages:

- Builds on an existing rshell embedding mechanism.
- Keeps network command implementations and release cadence in the Agent.
- May require less new public API than a complete extension framework.

Disadvantages:

- The current handler is intentionally generic and lacks typed registration, help, and operation-level mode metadata.
- Extending it may blur the boundary between reviewed Agent operations and arbitrary external execution.
- Static analysis and security review are harder when behavior lives outside rshell.
- It may evolve into a less explicit version of the general extension framework.

### Evaluation criteria

The decision should weigh:

- Preservation of rshell's default-deny security model.
- Ability to authorize investigation and remediation operations independently.
- Handler isolation and auditability.
- Implementation time for the v1 network command catalog.
- Release and compatibility coupling between rshell and the Agent.
- Reuse by Agent Health and other credible domains.
- Long-term clarity of ownership and testing.

## Solution-independent Network Architecture

### Overview

rshell exposes reviewed network-device operations implemented using the selected integration option. Each operation has defined syntax, typed arguments, an execution-mode requirement, and an Agent-owned implementation.

For a device operation, execution proceeds as follows:

1. rshell parses the shell program and resolves the requested network operation.
2. rshell verifies that the operation is enabled by the effective command policy.
3. The integration validates arguments and verifies that the current execution mode permits the operation.
4. The integration invokes the Agent-owned implementation with a bounded context and typed inputs.
5. The Agent resolves the opaque target through a device provider.
6. The provider obtains an authenticated connection without exposing credentials to rshell.
7. A vendor or protocol adapter performs the typed operation and returns a structured result.
8. The integration renders the result and records the exit status and telemetry.

### Responsibility boundary

rshell and the selected integration mechanism own:

- Command parsing and operation resolution.
- Command-policy enforcement.
- Argument-schema validation.
- Read-only and remediation-mode enforcement.
- Help and usage generation.
- Context cancellation, timeout propagation, and output bounds.
- Stable text and JSON rendering contracts.
- Integration-level telemetry and errors.

The Datadog Agent owns:

- Network-operation implementations and domain behavior.
- Opaque target resolution.
- Credential acquisition and isolation.
- Device connection lifecycle and locking.
- Protocol selection and vendor translation.
- Parsing native responses into structured results.
- Domain-level telemetry and audit context.

### Network operation contract

The selected integration must represent, for every operation:

- Its command syntax and description.
- Its typed arguments and validation rules.
- Whether it is investigative or remedial.
- Its command-policy and audit identity.
- Its bounded Agent-owned implementation.
- Its text, JSON, error, and exit-status contracts.

Invalid, duplicate, or ambiguous operation definitions must fail safely before the operation becomes available.

## Network Command and Authorization Requirements

### Executable command paths

Network operations use natural command paths:

```sh
device interfaces show edge-router-1
```

Each executable network operation must have a distinct policy and audit identity. Allowing an investigation operation must not implicitly allow a remediation operation, and adding a future operation must not silently expand an existing exact grant. The selected integration must define how those identities use or interact with `AllowedCommands` and how backend and operator policy are expressed and intersected.

### Execution modes

Every executable network operation declares its minimum mode:

- Investigation operations run in read-only and remediation modes.
- Remediation operations run only in remediation mode.

Mode classification is mandatory and is enforced before device execution. An Agent-owned network implementation cannot downgrade or bypass this check.

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

These commands validate typed reads, normalized cross-vendor results, operation-level authorization, and the Agent provider boundary.

### Remediation command

```text
device interface set-state TARGET INTERFACE up|down
```

This command validates remediation-mode enforcement, typed state changes, argument validation, and post-operation error reporting. Configuration rollback may continue to use NCM's existing dedicated action in v1.

The final names and detailed schemas may change during implementation, but the v1 catalog must retain equivalent coverage of read-only and state-changing operations.

## Output and Error Contract

Agent-owned operations return structured domain results. The selected rshell integration renders those results rather than passing through raw native device output.

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
- Integration failures that cannot be represented as normal command failures are returned through the embedding API.

## Security Model

The backend is responsible for deciding which operation should be requested and for applying product-level authorization and approval policy. The Agent and rshell provide an independent enforcement boundary.

Local protections include:

- Intersection of backend and operator `AllowedCommands` policies.
- Precise resolution and authorization of executable network operations.
- Mandatory investigation or remediation classification.
- Typed argument validation before dispatch.
- Explicit, opaque targets.
- No arbitrary native command payload.
- No credential arguments or results.
- Context cancellation and execution timeouts.
- Bounded output and resource use.
- Agent-owned connection policy and device-side least-privilege credentials.
- Auditable command identity, target identity, mode, outcome, and duration.

Agent-owned network implementations are privileged code and require the same security scrutiny as rshell builtins. The selected integration must not provide a way to execute arbitrary binaries or bypass rshell filesystem, environment, or process restrictions.

## Alternatives Considered

### A generic `device exec` command

A generic command could forward arbitrary native CLI text to a target. This offers maximum vendor coverage but turns one allowed rshell command into an unrestricted device-command tunnel. It weakens local defense in depth and makes investigation/remediation classification unreliable. It is explicitly rejected.

### Reimplement native device CLIs as rshell commands

rshell could parse vendor-native commands directly. This would require dynamic vendor grammars, stateful CLI contexts, and significant ongoing compatibility work. It would also create command collisions and couple rshell to network vendors. It is rejected in favor of typed capabilities and Agent adapters.

### Multi-target command handlers

Each command could accept a set of devices and define concurrency and partial-success behavior. This duplicates orchestration already available in rshell and the backend, complicates results and retries, and broadens blast radius. V1 uses single-target operations.

### Exact native output

Returning raw device output would resemble familiar CLI sessions but creates unstable, vendor-specific contracts and may expose unexpected content. V1 uses normalized structured results with native-inspired rendering.

## Compatibility and Migration

The selected integration must preserve existing rshell builtins, command identities, and allowlists. The network implementation introduces no behavior changes when its commands are not configured or enabled.

The Agent and rshell versions must agree on the selected integration contract. An Agent without the network implementation exposes no network-device operations.

Help output must list only enabled network operations. Detailed help must not expose or authorize disabled remediation operations.

## Testing and Validation

### rshell integration contract tests

The selected integration must demonstrate operation resolution, authorization, mode enforcement, argument validation, help, cancellation, output bounds, error mapping, and isolation. Option-specific tests are defined when the integration decision is made.

### Agent tests

- Target resolution through each supported provider.
- Credential non-disclosure.
- Connection lifecycle and per-device locking.
- Vendor translation and normalized results.
- Investigation and remediation implementations.
- Unsupported vendor, protocol, and capability behavior.
- End-to-end Private Action Runner execution where applicable.

### Compatibility tests

- Existing help and command behavior remain unchanged when network commands are not enabled.
- Network command identities and schemas remain stable across supported Agent upgrades.
- Agent and rshell version mismatches fail safely.

## Rollout and Observability

The proposed rollout is staged:

1. Select and implement the rshell integration option. If Option A is selected, approve its companion framework RFC first.
2. Add the Agent device-provider boundary and read-only v1 commands.
3. Pilot read-only network investigation with a limited set of vendors and protocols.
4. Add the v1 network remediation command behind explicit remediation-mode and command-policy configuration.
5. Expand the network command catalog and adapters based on demonstrated use cases.

Telemetry should record the canonical command identity, execution mode, duration, outcome, provider and adapter type, and a non-secret target identifier. It must not record credentials, native command payloads, or unredacted device output.

The Agent must be able to disable all network operations or individual operations through local configuration. Rolling back the Agent or removing the network integration removes the corresponding operations without changing rshell's core builtin behavior.

## Dependencies and Open Questions

- Which rshell integration option should this RFC select?
- Which device and credential provider should be implemented first: NCM, backend-provided connection information, or another Agent provider?
- Which vendors and protocols are required for the initial pilot?
- What stable structured schemas should each v1 device operation return?
- Which telemetry and audit events must be emitted by rshell, the Agent-owned implementation, and the backend?
- What evidence is required before remediation capabilities are enabled beyond a limited pilot?
