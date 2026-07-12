# Network Investigation and Remediation with rshell

## The problem

Datadog AI agents can use telemetry, topology, and configuration history to detect and reason about network incidents, but they cannot actively inspect operational state or remediate affected network devices from within a customer's private environment. Investigations therefore fall back to narrow predefined actions or human operators with direct device access. This limits an AI agent's ability to diagnose dynamic failures, correlate evidence across devices, respond to vendor-specific behavior, and complete remediation workflows.

The missing capability is not basic device connectivity. The Datadog Agent already runs inside customer networks, rshell provides a restricted execution environment, and Network Configuration Management (NCM) can connect to supported devices, collect configurations, and roll back changes. The gap is a safe, composable interface through which AI agents can perform bounded network investigation and remediation.

## Desired outcome

AI agents should be able to use rshell from a Datadog Agent to:

- Inspect interface, routing, neighbor, reachability, and configuration state.
- Invoke reviewed remediation operations and verify their outcome.
- Work across heterogeneous devices without depending on one vendor CLI or protocol.
- Compose multi-device workflows through rshell or backend orchestration.
- Receive stable, machine-readable results suitable for further reasoning.

The capability should cover both Agent-host diagnostics and operations performed on network devices. Individual device commands remain single-target and non-interactive; workflows coordinate multiple bounded operations rather than opening persistent sessions.

## Safety boundary

The solution must preserve the customer's security boundary:

- Credentials remain hidden from AI agents and rshell programs.
- Every operation is explicit, reviewed, and independently authorizable.
- Investigation and remediation are distinct execution modes.
- The Agent enforces local safeguards in addition to backend policy.
- Execution is time-bounded, cancellable, resource-bounded, and auditable.
- Targets are explicit; commands cannot discover and operate on an unrestricted fleet.
- Arbitrary native device-command passthrough is not supported.

Target resolution, credentials, connectivity, protocol handling, and vendor translation remain Agent-owned responsibilities. NCM-managed devices and backend-provided connection information are both possible sources and remain an open design choice.

## Proposed v1 scope

V1 should validate the end-to-end boundary with a deliberately small catalog:

- Show interfaces.
- Show routes.
- Show routing neighbors.
- Set an interface administratively up or down.

These operations exercise cross-vendor reads, structured results, precise authorization, read-only versus remediation enforcement, and Agent-owned device access. Configuration rollback can continue through NCM's existing dedicated action initially.

Results should default to stable, native-inspired text and optionally support JSON. Raw native output is excluded because it is vendor-dependent, difficult to validate, and may expose unexpected content.

## rshell integration decision

The network behavior above does not require one specific rshell implementation. This RFC review should select among three options:

| Option | Advantages | Tradeoffs |
|---|---|---|
| General extension framework | Reusable by Network Devices, Agent Health, and future Agent domains; uniform authorization, help, telemetry, and execution contracts | Largest initial design; requires a companion RFC; creates a generic privileged extension surface |
| Injected `DeviceExecutor` | Small, explicit, easier to audit, and likely the fastest network vertical slice | Adds network concepts to rshell; couples rshell and Agent releases; does not serve other domains |
| Adapt external-command handling | Builds on an existing rshell embedding point and keeps implementations in the Agent | Existing handler lacks typed metadata and mode semantics; risks blurring reviewed capabilities with arbitrary execution |

The decision should prioritize preservation of rshell's default-deny model, independent authorization of remediation operations, handler isolation, implementation time, release coupling, auditability, and credible reuse by Agent Health.

## Decisions requested

1. Confirm that bounded network investigation and remediation from the Datadog Agent is a capability worth pursuing.
2. Confirm the safety boundary and deliberately small v1 command catalog.
3. Select the rshell integration approach for detailed design and implementation.
4. Identify the initial credential/device provider and the vendors and protocols required for a pilot.

The full design and supporting analysis are in [the engineering RFC](rshell-networks.md).
