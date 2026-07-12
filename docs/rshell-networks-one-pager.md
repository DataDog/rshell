# Network Investigation and Remediation with rshell

## Overview

Datadog AI agents can use telemetry to detect and reason about network issues, but they cannot actively inspect operational state or remediate affected network devices from within a customer's private environment. This limits an AI agent's ability to diagnose dynamic failures, correlate evidence across devices, respond to vendor-specific behavior, and complete remediation workflows.

We propose using rshell as the safe, composable execution interface for network-device investigation and remediation. Running within the Datadog Agent, rshell would expose reviewed operations while preserving bounded execution and local safeguards.

## What this enables

An AI agent could investigate an incident by inspecting interface state and errors, routing tables, routing-protocol neighbors, reachability, and relevant configuration state. It could correlate those observations across devices and with Datadog telemetry, then verify whether the network has recovered.

The longer-term capability would also expose narrowly scoped remediation operations, such as enabling or disabling an interface or restoring a known-good configuration. AI agents would compose reviewed operations rather than receive unrestricted, interactive access to native device CLIs.

## Why rshell and the Datadog Agent

rshell already provides a restricted, composable shell designed for AI agents. The Datadog Agent already runs inside customer networks and provides the natural customer-side execution environment. Network Configuration Management (NCM) also demonstrates that the Agent can connect to supported devices, collect configurations, and perform rollback operations.

Together, these components provide a credible foundation: rshell can define and constrain what an AI agent may request, while the Agent owns private-network access, device connectivity, credentials, protocol handling, and vendor-specific behavior. The exact integration between rshell and Agent-owned operations remains an engineering decision covered by the full RFC.

## Safety principles

The capability must preserve the customer's security boundary:

- Credentials remain hidden from AI agents and rshell programs.
- Operations are explicit, reviewed, and independently authorizable.
- Execution is non-interactive, time-bounded, cancellable, resource-bounded, and auditable.
- Targets are explicit; commands cannot discover and operate on an unrestricted device fleet.
- The Agent enforces local safeguards in addition to backend policy.
- Arbitrary native device-command passthrough is not supported.

## First milestone

The first milestone should prove read-only investigation with a small set of representative operations:

- Show interfaces and interface health.
- Show routes.
- Show routing-protocol neighbors.

This milestone validates device access, cross-vendor normalization, authorization, structured results, and safe composition without introducing state-changing behavior. After that boundary is proven, a narrowly scoped remediation operation can validate the stronger controls required for changes.

## Ask

Align on pursuing rshell-based network investigation and remediation, starting with a read-only investigation milestone. With that alignment, the next step is to select the rshell integration approach, initial device-access mechanism, and vendors and protocols for a pilot.

The detailed requirements, options, and tradeoffs are in [the engineering RFC](rshell-networks.md).
