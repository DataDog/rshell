---
name: gtfobins-validate
description: "Validate shell builtins against GTFOBins attack patterns to ensure exploits are blocked by the sandbox"
argument-hint: "[command-name]"
allowedTools:
  - Read
  - Write
  - Edit
  - Glob
  - Grep
  - Agent
  - WebFetch
  - "Bash(go test *)"
  - "Bash(go build *)"
  - "Bash(go vet *)"
  - "Bash(gofmt *)"
  - "Bash(git *)"
  - "Bash(curl *)"
  - "Bash(RSHELL_BASH_TEST=1 go test *)"
---

Validate that the shell's builtins are protected against known GTFOBins exploitation techniques. If **$ARGUMENTS** is provided, validate only that command. Otherwise, validate all registered builtins.

---

## Overview

[GTFOBins](https://gtfobins.org/) documents Unix binaries that can be abused to bypass security restrictions. Since this shell is used by AI Agents, any GTFOBins technique that works would represent a sandbox escape. This skill systematically checks each builtin against its GTFOBins entry and verifies that every documented attack vector is blocked.

## Workflow

### Step 1: Identify builtins to validate

If a specific command was provided via `$ARGUMENTS`, validate only that command. Otherwise, read the builtin registry at `interp/builtins/builtins.go` and collect all registered command names.

### Step 2: Fetch GTFOBins data for each builtin

For each builtin, check if a GTFOBins entry exists:

1. **Offline first**: Check `resources/gtfobins/<command>.md`. If the offline resources directory does not exist, inform the user they can run `/download-posix-resources` to cache them locally.
2. **Online fallback**: If offline resources are not available, fetch from `https://gtfobins.org/gtfobins/<command>/`.
3. **No entry**: If GTFOBins has no page for a command (404), note it as "not listed in GTFOBins" and skip to the next command.

For each GTFOBins entry found, extract:
- **Functions**: The attack categories (File Read, File Write, File Download, File Upload, Shell, Reverse Shell, Bind Shell, SUID, Sudo, Capabilities, Limited SUID)
- **Techniques**: The specific commands/code snippets for each function
- **Flags used**: The specific flags referenced in attack techniques (e.g. `-c-0` for head, `-c+0` for tail, `--files0-from` for wc)

### Step 3: Classify each attack technique

For each GTFOBins technique found, classify it into one of these categories:

| Category | Description | Action |
|----------|-------------|--------|
| **Blocked by design** | The shell never executes host binaries, so SUID/Sudo/Capabilities attacks are inherently impossible | Document as N/A |
| **Blocked by sandbox** | The technique reads/writes files, but the AllowedPaths sandbox restricts file access | Verify with a test |
| **Blocked by flag rejection** | The technique requires a flag the shell rejects (e.g. `--follow`, `--files0-from`) | Verify with a test |
| **Potentially exploitable** | The technique uses only flags/features the builtin supports and could work within the sandbox | Flag as **critical** — needs investigation |

### Step 4: Write validation tests

Create or update the pentest test file for each validated builtin:

**File**: `interp/builtins/<command>/builtin_<command>_pentest_test.go`

For each GTFOBins technique that is not "Blocked by design", write a Go test that:

1. Attempts the exact GTFOBins technique (adapted for the shell's syntax)
2. Verifies the attack is blocked (exit code 1, appropriate error message, or sandbox restriction)
3. Documents the GTFOBins source in a comment

Use this naming convention for GTFOBins-specific tests:
```go
// TestCmdGTFOBinsFileRead verifies that the GTFOBins file-read technique
// for <command> is blocked by the sandbox.
//
// GTFOBins: https://gtfobins.org/gtfobins/<command>/
// Technique: <command> <flags> /path/to/input-file
func TestCmdGTFOBinsFileRead(t *testing.T) {
    // ...
}
```

#### Test patterns by attack category

**File Read via sandbox escape** — Verify the command cannot read files outside AllowedPaths:
```go
func TestCmdGTFOBinsFileReadSandboxEscape(t *testing.T) {
    allowed := t.TempDir()
    secret := t.TempDir()
    require.NoError(t, os.WriteFile(filepath.Join(secret, "secret.txt"), []byte("secret data"), 0644))
    secretPath := filepath.ToSlash(filepath.Join(secret, "secret.txt"))
    // Attempt the GTFOBins technique targeting a file outside the sandbox
    _, stderr, code := cmdRun(t, "<command> "+secretPath, allowed)
    assert.Equal(t, 1, code)
    assert.Contains(t, stderr, "<command>:")
}
```

**File Read via dangerous flags** — Verify flags used in GTFOBins techniques are rejected:
```go
func TestCmdGTFOBinsFlagRejected(t *testing.T) {
    dir := t.TempDir()
    writeFile(t, dir, "f.txt", "data\n")
    _, stderr, code := cmdRun(t, "<command> <dangerous-flag> f.txt", dir)
    assert.Equal(t, 1, code)
    assert.Contains(t, stderr, "<command>:")
}
```

**File Write / Shell / Reverse Shell** — These should be inherently impossible (no write redirections, no exec), but verify the specific technique fails:
```go
func TestCmdGTFOBinsShellEscapeImpossible(t *testing.T) {
    dir := t.TempDir()
    // Attempt the GTFOBins shell escape technique
    _, stderr, code := cmdRun(t, "<gtfobins-technique>", dir)
    assert.Equal(t, 1, code)
    // The command or flag should be rejected
}
```

### Step 5: Run tests and verify

Run the tests to confirm all GTFOBins techniques are blocked:

```bash
go test ./interp/... -run TestCmdGTFOBins -timeout 120s -v
```

Fix any test failures. If a GTFOBins technique is **not** blocked, this is a critical security finding — flag it immediately.

### Step 6: Generate validation report

Output a summary table:

```
## GTFOBins Validation Report

| Command | GTFOBins Entry | Functions | Status |
|---------|---------------|-----------|--------|
| cat     | Yes           | File Read | All blocked |
| echo    | No            | N/A       | Not listed |
| head    | Yes           | File Read | All blocked |
| tail    | Yes           | File Read | All blocked |
| wc      | Yes           | File Read | All blocked |
| ...     | ...           | ...       | ...    |
```

For each technique tested, include:
- The GTFOBins URL
- The specific technique/command
- How it is blocked (sandbox, flag rejection, or by design)
- The test function name that validates it

### Critical findings

If any GTFOBins technique is found to be exploitable:

1. **Stop and report immediately** — do not continue validation
2. Describe the exact attack vector and impact
3. Suggest a fix (flag rejection, sandbox enforcement, etc.)
4. After the fix is applied, re-run validation

## Known GTFOBins attack patterns for current builtins

This section documents the specific GTFOBins techniques relevant to rshell's builtins, for reference:

### cat
- **File Read**: `cat /path/to/file` — blocked by AllowedPaths sandbox

### head
- **File Read**: `head -c-0 /path/to/file` — the `-c-0` flag (negative byte count, meaning "all bytes") must be rejected since we don't support negative counts

### tail
- **File Read**: `tail -c+0 /path/to/file` — the `+0` offset mode is supported but file access is restricted by AllowedPaths sandbox

### wc
- **File Read**: `wc --files0-from /path/to/file` — the `--files0-from` flag must be rejected (not implemented)

### echo
- **Not listed** in GTFOBins

### true / false / exit / break / continue
- **Not listed** in GTFOBins (no file access capabilities)

## Notes

- SUID, Sudo, and Capabilities attack vectors are **always N/A** for this shell because it never executes host binaries — all commands are Go builtins running in-process.
- File Write, File Download, File Upload, Shell, Reverse Shell, and Bind Shell functions are inherently blocked because the shell has no write redirections (`>`, `>>` are blocked), no `exec`, no network access, and no process spawning.
- The primary attack surface is **File Read** — ensuring the AllowedPaths sandbox cannot be bypassed and that dangerous flags (which could expand what files are readable) are rejected.
