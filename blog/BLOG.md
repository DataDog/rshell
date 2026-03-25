<p align="center">
  <img src="../assets/rshell-logo-text.png" alt="rshell logo" width="600"/>
</p>

# We built a restricted shell for AI agents. AI wrote most of it.

In about ten days, a small team merged 100 pull requests, shipped 20,000 lines of production Go, and wrote 4,500 tests for a POSIX-compatible shell interpreter. It's now open source at [github.com/DataDog/rshell](https://github.com/DataDog/rshell). Almost all of it was written and reviewed by AI.

I realize that raises obvious questions. How do you maintain code quality at that pace? Why would you trust AI-generated code for something security sensitive? And why write a shell from scratch?

## Why AI agents need a shell

At Datadog, AI agents investigate production incidents. They need to dig through on-host data, log files, proc filesystems, network state, and a lot of that work has to happen locally. You can't always ship gigabytes of raw logs to a backend for analysis.

LLMs are trained on POSIX shell. When an agent needs to diagnose something, it reaches for `grep`, `find`, `tail`, pipes, loops. That's the right instinct. Pre-defined scripts are too rigid. Custom MCP tools require modeling every diagnostic workflow in advance. Shell scripting lets the agent improvise.

But giving an agent a real `bash` session on a production host is a non-starter. Standard POSIX tools carry risks that aren't obvious at first glance: `find` and `sed` can execute arbitrary binaries, `sort` can write to the filesystem, `grep`'s default regex engine can trivially DoS a machine, and `tail -n 9999999999999999` will OOM a host through greedy buffer allocation. Even the shell itself is a risk: a malicious binary planted on `$PATH` can silently replace any command. One prompt injection could turn an investigative agent into an attacker with full access.

We needed agents to read log files, filter text, and inspect system state, but not execute binaries, write to disk, or open network connections. So we built a shell that only knows the commands we taught it, only accesses the paths we explicitly allow, and blocks everything else by default.

## Designing the restricted shell

The embedding application explicitly grants access to specific commands, filesystem paths, and environment variables. No external binaries. No filesystem writes. No network connections. Dangerous shell constructs like background execution and write redirections are rejected at parse time before any code runs. Everything the agent can do is opted into; everything else is blocked.

### Parser and interpreter, separated

Shell scripts are parsed into an AST using [mvdan/sh](https://github.com/mvdan/sh), a well-maintained Go shell parser. We forked its interpreter and rebuilt it around our security model. Because we control the interpreter, we can allow `for` loops and `if` clauses while blocking `exec` and `eval`. Unknown syntax gets rejected at the grammar level before anything runs. The interpreter supports pipes, command substitution, variable expansion, globbing, enough to be genuinely useful without the features that make `bash` dangerous in untrusted contexts.

### Builtins, not host binaries

Every command, `cat`, `grep`, `find`, `sed`, `ss`, `ip`, and twenty more, is reimplemented as a Go function. The interpreter never calls a host binary. This buys us a few things.

File access enforcement: every file open goes through an `AllowedPaths` sandbox check using Go 1.24's [`os.Root` API](https://go.dev/blog/osroot). `os.Root` confines all file operations to a directory tree. Symlinks that escape are blocked, `..` cannot traverse out, and on Unix `openat` syscalls eliminate TOCTOU races. If the path isn't on the allowlist, the operation fails, on Linux, macOS, and Windows alike.

Cross platform consistency: the same `grep` implementation runs everywhere. No surprises from GNU vs. BSD flag differences. No missing utilities.

No supply chain risk: a malicious `ls` placed on `$PATH` is invisible to the shell. There is no `$PATH` to search. The interpreter dispatches directly to our Go functions.

### Layered security

The first layer is always on: interpreter restrictions, builtin-only execution, the path allowlist. A planned second layer adds OS-level sandboxing (Landlock on Linux, App Sandbox on macOS) for defense in depth.

Between these layers sits a library function allowlist. Every builtin has an explicit list of permitted Go standard library functions. `os.Open` is allowed, `os/exec.Command` is not. CI enforces that no builtin calls a function outside its allowlist, and any change to the allowlists requires a human to review and approve before merging. AI can implement commands, but it can't grant itself access to new capabilities.

The tradeoff: we own the implementation risk. Every builtin we write could have a bug. We mitigate that with testing, automated review, the library function allowlist that a human signs off on, and a development process that catches issues early.

## The AI harness

AI did the coding. Getting that to work consistently across 25 commands, 100 PRs, and a security-sensitive codebase required structure.

### Why ad-hoc prompting breaks down

Using an AI assistant to write individual functions works fine. But when you have 25 commands to implement, each with its own POSIX spec, security surface, test suite, and review cycle, ad-hoc prompting stops working. You get inconsistency across implementations. Edge cases get missed in some commands but not others. Review fatigue sets in because every PR looks different.

### Skills as repeatable workflows

We built a set of "skills," structured step-by-step workflows that Claude Code follows for every command. The `implement-posix-command` skill defines a ten-step protocol:

1. Research the command: read the POSIX spec, study GTFOBins attack patterns
2. Confirm flag selection with the human before writing any code
3. Write scenario tests: POSIX-compliant tests validated against real `bash`
4. Write Go unit tests covering edge cases and error paths
5. Implement the command following the approved spec from step 2
6. Verify and harden: run all tests, fix failures, iterate
7. Code review: automated, multi-pass, covering security and correctness
8. Exploratory pentest: attack the command with specific categories (path traversal, integer overflow, infinite sources, flag injection)
9. Write fuzz tests for additional coverage beyond structured tests
10. Update documentation to match the implementation

Steps 3, 4, and 5 run in parallel since the tests and the implementation are both driven by the approved spec from step 2.

Backing the skills is a shared rules file that codifies every security invariant a builtin must satisfy: bounded buffers for untrusted input, regex execution limits to prevent ReDoS, integer overflow checks on all numeric arguments, sandbox-only file access, cross-platform path handling, and more. The rules file acts as a machine-readable security policy. The AI follows it on every implementation, and reviewers audit against it. When we found a gap in the rules, we fixed it once and every future command inherited the fix.

Every command went through this same pipeline. That consistency made review tractable. By the twentieth builtin, the reviewer already knew the structure, the test patterns, and the security invariants to check.

### The review-fix loop

For pull requests, a separate `review-fix-loop` skill runs code review, addresses comments, fixes CI failures, and iterates until the PR is clean without requiring a human in the loop for each cycle. The skill coordinates self-review in parallel with external review requests, then comment resolution, CI fixes, and a decision on whether another iteration is needed. The human approves the design; the harness handles the grunt work.

### What the human actually does

The human engineer's role shifted. Less time writing code. More time on:

- Defining security rules and invariants that the harness enforces
- Approving flag selections and design decisions before implementation starts
- Verifying library function allowlists, since every PR that adds new stdlib functions to a builtin's allowlist requires explicit human approval. AI-generated code can't quietly expand its own capabilities.
- Reviewing the harness itself. The skills are code too, and a bug in a skill replicates across every command it implements.
- Handling what the harness couldn't: novel security questions, cross-cutting architecture decisions, judgment calls about scope

I wrote very little code on this project. Mostly I was deciding what should exist and how it should behave, then verifying the AI got it right.

## What we learned

Gaps in the harness replicate. This one bit us. When a skill didn't enforce a security rule consistently, that gap showed up in every command built with it. Fixing the skill retroactively meant re-reviewing work we thought was done. We ended up frontloading a lot of effort into the skill definitions because a ten-minute fix there saves hours across twenty implementations.

AI-generated tests caught AI-generated bugs. I didn't expect this to work, but it does, because the test suite was written from POSIX specs and GNU coreutils reference tests, not from the implementation. The spec and the code are different representations of the same behavior. When they disagree, that's a real bug.

"Review this for security issues" doesn't cut it. Open-ended security prompts give you inconsistent results. What worked was the pentest step in our skill, which forces specific attack categories on every command: path traversal, symlink exploitation, integer overflow, infinite sources, flag injection, filename-as-flag injection. A checklist that runs every time beats a sharp reviewer who sometimes forgets to check something.

We assumed safety would slow us down. It didn't. Security checks baked into the workflow meant issues got caught at the PR level instead of surfacing weeks later. We moved faster because we weren't piling up hidden risk.

## Results

The numbers:

- ~10 days from first commit to production-ready
- 100 PRs merged
- 25 shell commands implemented
- 20,000 lines of production Go (excluding tests)
- 4,500 tests: 2,000 unit tests and 2,500 scenario tests validated against real `bash`
- ~100% AI-generated code; most of the code reviewed by AI

The result is a cross platform, sandboxed POSIX shell interpreter that AI agents can use to diagnose production systems, with a security model operators can configure and audit. It's open source at [github.com/DataDog/rshell](https://github.com/DataDog/rshell).

## What's next

We're expanding the command set and working on OS-level sandboxing: Landlock on Linux, App Sandbox on macOS. The shell will be integrated into the Datadog Agent as an MCP tool exposed over PAR (Private Action Runner), giving AI agents a secure execution environment on any monitored host.

Honestly, the two halves of this project turned out to be the same problem. Letting an AI agent run commands on a production host, letting AI write security-sensitive code: both come down to whether you trust the guardrails enough to let the thing actually do its job. We found that you don't get there by locking everything down. You get there by building a process that catches mistakes before they matter.
