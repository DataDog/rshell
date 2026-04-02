# Datadog Cloud Shell

## Overview

Datadog Cloud Shell brings AWS CloudShell-style terminal access directly into the Datadog platform — a secure, auditable, browser-based shell for interacting with customer infrastructure without requiring direct SSH access or cloud provider console logins.

### The Problem

When engineers are in the middle of triaging or remediating an incident, context-switching to SSH sessions, cloud consoles, or VPN tunnels creates friction and breaks the investigation flow. Access control is inconsistent, approval workflows are manual, and there is no native audit trail tied to the observability data that triggered the investigation in the first place.

### What Datadog Cloud Shell Enables

**Incident triage and remediation, in context.** Engineers can run diagnostic commands and remediation scripts directly from Datadog, alongside the dashboards, logs, and traces that surfaced the issue — no tab-switching, no separate credential management.

**Access control and auditability, built in.** Because the shell runs through Datadog, access policies, approval workflows, and a full audit log of every command executed come for free. This is meaningfully simpler than managing SSH keys or cloud IAM policies per team.

**Platform-agnostic reach.** Cloud Shell works across AWS, GCP, Azure, on-premise servers, and physical devices — steering customers toward a single, governed access path instead of a patchwork of SSH tunnels and cloud-specific consoles.

### Strategic Fit

This is a natural extension of Datadog's position as the all-in-one platform. By inserting Datadog into the command execution layer of the DevOps loop, we gain:

- Customers can interact with their own infrastructure and applications directly from Datadog
- A compelling reason for customers to consolidate even more of their operations workflow in Datadog
- Synergy with ongoing RCA and remediation workstreams

The shell (https://github.com/DataDog/rshell) itself is already built — this document makes the case for shipping it as a new feature.
