# Shell Power, Agent Safety: How We Built a Restricted Shell Interpreter—Mostly Using AI

In roughly ten days, a small team merged 100 pull requests, shipped 20,000 lines of production Go, and wrote 4,500 tests for a POSIX-compatible shell interpreter—now open source at [github.com/DataDog/rshell](https://github.com/DataDog/rshell). Almost all of it was written and reviewed by AI.

That sentence probably raises more questions than it answers. How do you maintain code quality at that pace? How do you trust AI-generated code in a security-sensitive project? And why build a shell from scratch in the first place?

The answers turned out to be connected in ways we didn't expect.

## Why AI Agents Need a Shell

At Datadog, AI agents investigate production incidents. They need to explore large volumes of on-host data—log files, proc filesystems, network state—and much of that work has to happen locally. Sending gigabytes of raw log data to a backend for analysis isn't always viable.

LLMs are trained on POSIX shell. When an agent needs to diagnose an issue, it naturally reaches for `grep`, `find`, `tail`, pipes, and loops. These are the composable primitives that make ad-hoc investigation possible. Pre-defined scripts are too rigid. Custom MCP tools require modeling every diagnostic workflow in advance. Shell scripting lets the agent think on its feet.

But the naive answer—giving the agent a real `bash` session on a production host—is a non-starter. Unrestricted shell access is an unacceptable attack surface. A single prompt injection could turn an investigative agent into an attacker.

The insight that launched this project: what if we built a shell that only knows the commands we taught it, and only accesses the paths we explicitly allow?

## Designing the Restricted Shell

We needed a shell that was powerful enough to be useful and constrained enough to be safe. Three design decisions shaped the architecture.

### Parser and interpreter, separated

Shell scripts are parsed into an AST using [mvdan/sh](https://github.com/mvdan/sh), a well-maintained Go shell parser. But instead of handing that AST to a standard interpreter, we wrote our own. This separation is the foundation of the security model: we control every operation. We can allow `for` loops and `if` clauses while blocking `exec` and `eval`. Unknown syntax is rejected at the grammar level before anything runs. The interpreter supports pipes, command substitution, variable expansion, globbing—enough shell to be genuinely useful, without the features that make `bash` dangerous in untrusted contexts.

### Builtins, not host binaries

Every command—`cat`, `grep`, `find`, `sed`, `ss`, `ip`, and twenty more—is reimplemented as a Go function. The interpreter never calls a host binary. This gives us three properties that would be impossible otherwise:

**File access enforcement.** Every file open goes through an `AllowedPaths` sandbox check. It doesn't matter how creative the shell script is—if the path isn't on the allowlist, the operation is denied. This works at the Go function level, not at the OS level, so it's consistent across platforms.

**Cross-platform consistency.** The same `grep` implementation runs on Linux, macOS, and Windows. No surprises from different GNU vs. BSD flag behavior. No missing utilities.

**No supply-chain risk.** A malicious `ls` placed on `$PATH` is invisible to the shell. There's no `$PATH` to search. The interpreter dispatches directly to our Go functions.

### Layered security

The first layer is always on: interpreter restrictions, builtin-only execution, and the path allowlist. A planned second layer adds OS-level sandboxing (Landlock on Linux, App Sandbox on macOS) for defense in depth.

The tradeoff we accepted is clear: we own the implementation risk. Every builtin we write could have a bug. The mitigation: rigorous testing, automated code review, and a development process designed to catch issues before they ship.

## The AI Harness: Building with AI at Scale

This is the part that surprised us. Not that AI could write the code—but that the *process* of structuring AI-assisted development turned out to be more important than the AI itself.

### The problem with ad-hoc AI coding

Using an AI assistant to write individual functions is useful. But when you have 25 commands to implement, each with its own POSIX spec, security surface, test suite, and review cycle, ad-hoc prompting doesn't scale. You get inconsistency across implementations. Edge cases get missed in some commands but not others. Review fatigue sets in because every PR looks different.

### Skills as repeatable workflows

We built a set of *skills*—structured, step-by-step workflows that Claude Code follows for every command. The `implement-posix-command` skill defines a ten-step protocol:

1. **Research the command** — read the POSIX spec, study GTFOBins attack patterns
2. **Confirm flag selection** with the human before writing any code
3. **Write scenario tests** — POSIX-compliant tests validated against real `bash`
4. **Write Go unit tests** — covering edge cases and error paths
5. **Implement the command** — following the approved spec from step 2
6. **Verify and harden** — run all tests, fix failures, iterate
7. **Code review** — automated, multi-pass, covering security and correctness
8. **Exploratory pentest** — attack the command with specific categories: path traversal, integer overflow, infinite sources, flag injection
9. **Write fuzz tests** — for additional coverage beyond structured tests
10. **Update documentation** — keep feature docs in sync with implementation

Steps 3, 4, and 5 run in parallel—the tests and the implementation are both driven by the approved spec from step 2, so they don't need to wait for each other.

Every command went through this same pipeline. The consistency is what made review tractable: by the time a reviewer looked at the twentieth builtin, they already knew the structure, the test patterns, and the security invariants to check.

### The review-fix loop

For pull requests, a separate `review-fix-loop` skill runs code review, addresses comments, fixes CI failures, and iterates until the PR is clean—without requiring a human in the loop for each cycle. The skill coordinates multiple phases: self-review in parallel with external review requests, then comment resolution, CI fixes, and a decision on whether another iteration is needed. The human approves the design; the harness handles the iteration.

### What the human actually does

The role of the human engineer shifted. Less time writing code. More time on:

- **Defining the security rules** and invariants that the harness enforces
- **Approving flag selections** and design decisions before implementation starts
- **Reviewing the harness itself** — the skills are code too, and a bug in a skill replicates across every command it implements
- **Handling the cases the harness couldn't** — novel security questions, cross-cutting architectural decisions, judgment calls about scope

The human became the architect of the process rather than the author of the code.

## What Surprised Us

**The harness is load-bearing infrastructure.** When a skill had a gap—say, it didn't enforce a security rule consistently—that gap replicated across every command implemented with it. Fixing the skill retroactively meant re-reviewing work already done. We learned to invest heavily in the skill definitions upfront. The compound interest is real: a ten-minute improvement to a skill saves hours across twenty command implementations.

**AI-generated tests caught AI-generated bugs.** Because the test suite was written from POSIX specs and GNU coreutils reference tests—sources independent of the implementation—it was genuinely independent. Bugs that an AI introduced in the implementation were caught by tests the same AI wrote from the spec. This felt counterintuitive at first. It works because the spec and the implementation are different representations of the same behavior, and mismatches between them surface real bugs.

**Security review needs structure, not just a prompt.** Asking an AI to "review this for security issues" produces inconsistent results. The pentest step in the skill forces specific attack categories on every command: path traversal, symlink exploitation, integer overflow, infinite sources, flag injection, filename-as-flag injection. Structure beats exhortation. A checklist that runs every time is more reliable than a brilliant reviewer who sometimes forgets.

**Velocity and safety reinforced each other.** The assumption going in was that safety would slow things down. In practice, having automated security checks baked into the workflow meant we could move faster without accumulating risk. Issues were caught at the PR level rather than discovered later in a security audit. The harness made speed and safety complements, not tradeoffs.

## Results

The numbers, plainly:

- ~10 days from first commit to production-ready
- 100 PRs merged
- 25 shell commands implemented
- 20,000 lines of production Go (excluding tests)
- 4,500 tests: 2,000 unit tests and 2,500 scenario tests validated against real `bash`
- ~100% AI-generated code; most of the code reviewed by AI

What this enabled: a fully cross-platform, sandboxed POSIX shell interpreter that AI agents can use to diagnose production systems, with a security model that operators can configure and audit. The project is open source at [github.com/DataDog/rshell](https://github.com/DataDog/rshell).

## What's Next

We're continuing to expand the command set and working on OS-level sandboxing layers—Landlock on Linux, App Sandbox on macOS—to add defense in depth beyond the interpreter-level protections. The shell will be integrated into the Datadog Agent as an MCP tool exposed over PAR (Private Action Runner), giving AI agents a secure execution environment on any monitored host. Further out, we're exploring a remote shell UI in Fleet Automation, letting users inspect their hosts directly from the Datadog platform.

The through-line of this project turned out to be *trust*. We built a shell that gives AI agents real power with real constraints—enough to investigate production incidents, not enough to cause them. And we built a development process that gives AI systems real autonomy with real guardrails—enough to ship 100 PRs in ten days, not enough to skip security review.

The two problems turned out to be the same problem. In both cases, the answer wasn't to restrict capability until it was safe. It was to build the right structure around capability so that safety comes from the process, not from limitation.
