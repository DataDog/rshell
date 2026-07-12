# Network Investigation and Remediation with rshell

## Overview

Datadog AI agents can use telemetry to detect and reason about network issues, but they cannot actively inspect operational state or remediate affected network devices from within a customer's private environment. This limits an AI agent's ability to diagnose dynamic failures, correlate evidence across devices, respond to vendor-specific behavior, and complete remediation workflows.

We propose using rshell as the safe, composable execution interface for network-device investigation and remediation. Running within the Datadog Agent, rshell would expose reviewed operations while preserving bounded execution and local safeguards.
