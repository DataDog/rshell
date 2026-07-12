# Network Investigation and Remediation with rshell

## Overview

Datadog AI agents can use telemetry to detect and reason about network issues, but they cannot actively inspect operational state or remediate affected network devices from within a customer's private environment. This limits an AI agent's ability to diagnose dynamic failures, correlate evidence across devices, respond to vendor-specific behavior, and complete remediation workflows.

We propose using rshell as the safe, composable execution interface for network-device investigation and remediation. Running within the Datadog Agent, rshell would expose restricted operations while preserving bounded execution and local safeguards.

## What this enables

An AI agent could investigate an incident by inspecting interface state and errors, routing tables, routing-protocol neighbors, reachability, and relevant configuration state. It could correlate those observations across devices and with Datadog telemetry, then verify whether the network has recovered.

The longer-term capability would also expose narrowly scoped remediation operations, such as enabling or disabling an interface or changing configurations. AI agents would compose restricted operations rather than receive unrestricted, interactive access to native device CLIs.

## Why rshell and the Datadog Agent

rshell already provides a restricted, composable shell designed for AI agents. The Datadog Agent already runs inside customer networks and provides the natural customer-side execution environment. Network Configuration Management (NCM) also demonstrates that the Agent can connect to supported devices.

Together, these components provide a credible foundation: rshell can define and constrain what an AI agent may request, while the Agent owns private-network access, device connectivity, credentials, protocol handling, and vendor-specific behavior. The exact integration between rshell and Agent-owned operations remains an engineering decision covered by the full RFC.

## Safety principles

The capability must preserve the customer's security boundary:

- Credentials remain hidden from AI agents and rshell programs.
- Operations are explicit, reviewed, and independently authorizable.
- The Agent enforces local safeguards (only expose subset of device commands) in addition to backend policy.
- Arbitrary native device-command passthrough is not supported.

## Milestones

### M1: Read-only device investigation via rshell

Example operations:
- Show interfaces
- Show routes

This milestone validates device access, cross-vendor normalization and safe composition via rshell.

### M2: Remediation device investigation via rshell

