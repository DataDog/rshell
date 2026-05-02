---
name: datadog/remote-host-diagnostics
description: Diagnose remote hosts through ./rshell only
toolsets: core
---

# Remote Host Diagnostics

Use only `./rshell`, and keep all actions read-only, bounded, and limited to prompt-provided hosts, paths, or roots. Inspect files through `./rshell` rather than local filesystem access.

Workflow:
- Start with `./rshell --help` and command/builtin help when flags or behavior are uncertain; adapt to the available interface.
- Gather targeted context for the reported symptom, then correlate across relevant layers (service status, config, logs, resources, network) without broad or repetitive searches.
- Separate likely causal evidence from incidental noise, and stop once the finding is supported or the remaining gap is clear.
- In the final answer, list key commands run, cite concrete file/output observations, state the likely cause with confidence, and note missing evidence or safe next steps if ambiguous.
