# Network Investigation and Remediation with rshell

## Problem Statement

Datadog AI agents can detect and reason about network incidents using telemetry, topology, and configuration history, but they lack a secure way to actively investigate and remediate the underlying network from within a customer's private environment.

Existing capabilities address parts of this problem: the Datadog Agent provides a customer-side execution environment, rshell safely executes constrained operations, and Network Configuration Management (NCM) communicates with network devices and supports configuration collection and rollback. The Private Action Runner is an existing Datadog Agent mechanism for receiving and executing actions from Datadog within a customer's private network. However, there is no general execution interface that lets an AI agent perform open-ended host diagnostics, issue native vendor-specific commands, coordinate operations across multiple network devices, and verify remediation outcomes.

Without this capability, investigations must fall back to predefined actions or human operators with direct device access. This limits an AI agent's ability to diagnose dynamic operational state, correlate observations across devices, respond to the long tail of vendor-specific failures, and complete remediation workflows.

The challenge is to enable bounded, auditable, protocol-agnostic network investigation and remediation from a Datadog Agent while preserving the customer's security boundary. Operations must support heterogeneous devices and multi-device workflows, keep credentials hidden from AI agents, remain constrained by backend policy and independent local safeguards, and execute as isolated, time-bounded actions rather than unrestricted interactive access.
