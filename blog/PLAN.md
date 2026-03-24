# Blog Post Plan: How We Built rshell with AI

## Working Title

**"Shell Power, Agent Safety: How We Built a Restricted Shell Interpreter—Mostly Using AI"**

---

## Goal

A "we built this" + "lessons learned" post for Datadog engineers and AI engineers.
~1500–2500 words, no code samples, conceptual/narrative. To be published on the Datadog engineering blog and internally.

---

## Narrative Arc

The post has two intertwined threads:

1. **The product story**: We needed a shell that AI agents can use safely. We designed one from scratch with a layered security model.
2. **The meta story**: We built it almost entirely with AI—and the process of doing that taught us something surprising about how to structure AI-assisted engineering at scale.

The through-line is *trust*: how do you give an AI agent enough power to be useful without trusting it blindly? That question applies both to the shell we built (what can the agent do?) and to the way we built it (how much do we trust AI-generated code?).

---

## Sections

### 1. Hook / Opening (~150 words)

Open with the concrete result: in roughly ten days, a small team merged 100 pull requests, shipped 20,000 lines of production Go, and wrote 4,500 tests for a POSIX-compatible shell interpreter. Almost all of it was written and reviewed by AI.

Then ask the question that makes it interesting: how do you actually pull that off without the whole thing falling apart?

### 2. The Problem: Why AI Agents Need a Shell (~250 words)

Set up the motivation from the RFC.

- AI agents investigating production incidents need to explore large volumes of on-host data (logs, proc files, network state). Sending that data to the backend isn't always viable.
- LLMs are trained on POSIX shell—they naturally reach for `grep`, `find`, `tail`, pipes, loops.
- The naive answer—give the agent a real shell—is a non-starter: unrestricted `bash` on a production host is an unacceptable attack surface.
- Existing alternatives (pre-defined scripts, custom MCP tools) either don't compose well or require modeling every diagnostic workflow in advance.

The insight: what if we built a shell that only knows the commands we taught it, and only accesses the paths we explicitly allow?

### 3. Designing the Restricted Shell (~400 words)

Walk through the key design decisions (draw from RFC). Keep it conceptual—explain the *why*, not the *how*.

**A. Parser vs. interpreter**
We use `mvdan/sh` to parse shell scripts into an AST, then execute that AST through our own interpreter written in Go. This means we control every operation: we can allow `for` loops and `if` clauses, block `exec` and `eval`, and reject unknown syntax at the grammar level before anything runs.

**B. Builtins, not host binaries**
Every command—`cat`, `grep`, `find`, `sed`, `ss`, `ip`—is reimplemented as a Go function. The interpreter never calls a host binary. This gives us:
- File access enforcement: every file open goes through an `AllowedPaths` sandbox check.
- Cross-platform consistency: the same `grep` runs on Linux, macOS, and Windows.
- No supply-chain risk: a malicious `ls` on `$PATH` is invisible to the shell.

**C. Layered security**
Layer 1 (always): interpreter restrictions + builtin-only execution + path allowlist.
Layer 2 (optional, per-platform): OS-level sandboxing (Landlock on Linux, App Sandbox on macOS, Job Objects on Windows) for additional depth-in-defense.

The tradeoff we accepted: we own the implementation risk. Every builtin we write could have a bug. The mitigation: rigorous testing, automated code review, and a development harness designed to catch issues early.

### 4. The AI Harness: Building with AI at Scale (~500 words)

This is the heart of the post. Explain the workflow that made 100 PRs in 10 days possible.

**The problem with ad-hoc AI coding**
Using an AI assistant to write individual functions is useful. But when you have 25 commands to implement, each with its own POSIX spec, security surface, test suite, and review cycle, ad-hoc prompting doesn't scale. You get inconsistency, missed edge cases, and review fatigue.

**Skills as repeatable workflows**
We built a set of *skills*—structured, step-by-step workflows that Claude Code follows for every command. The `implement-posix-command` skill, for example, defines a 10-step protocol:
1. Research the command (POSIX spec, GTFOBins attack patterns)
2. Confirm flag selection with the human
3. Write scenario tests, Go unit tests, and the implementation in parallel
4. Harden (run tests, fix failures)
5. Code review (automated, multi-pass)
6. Exploratory pentest (attack the command the way an adversary would)
7. Write fuzz tests
8. Update documentation

Every command went through this same pipeline. The consistency is what made review tractable: a reviewer looking at the 20th builtin already knows the structure, the test patterns, the security invariants to check.

**The review-fix loop**
For pull requests, a separate `review-fix-loop` skill runs code review, addresses comments, fixes CI failures, and iterates until the PR is clean—without requiring a human in the loop for each cycle. The human approves the design; the harness handles the iteration.

**What the human actually does**
The role of the human engineer shifted: less time writing code, more time on:
- Defining the security rules and invariants that the harness enforces
- Approving flag selections and design decisions before implementation starts
- Reviewing the harness itself (the skills are code too)
- Handling the cases the harness couldn't (novel security questions, cross-cutting architectural decisions)

### 5. What Surprised Us (~300 words)

Honest "lessons learned" section. Suggestions:

- **The harness is load-bearing infrastructure.** When a skill had a gap (e.g., didn't enforce a security rule consistently), that gap replicated across every command implemented with it. Fixing the skill retroactively meant re-reviewing work already done. Investing in the skill upfront pays compound interest.

- **AI-generated tests caught AI-generated bugs.** Because the test suite was also AI-generated (from POSIX specs and GNU coreutils reference tests), it was genuinely independent from the implementation. Bugs that an AI wrote into the implementation were caught by tests the same AI wrote from the spec. This felt counterintuitive at first.

- **Security review needs to be structured, not just asked for.** Asking an AI to "review this for security issues" produces inconsistent results. The pentest step in the skill forces specific attack categories (path traversal, integer overflow, infinite sources, flag injection) on every command. Structure beats exhortation.

- **Velocity and safety reinforce each other when the harness is right.** The assumption going in was that safety would slow things down. In practice, having automated security checks in the workflow meant we could move faster without accumulating risk, because issues were caught at the PR level rather than discovered later.

### 6. Results (~150 words)

State the numbers plainly:
- ~10 days from first commit to the state described here
- 100 PRs merged
- 25 shell commands implemented
- 20,000 lines of production Go
- 4,500 tests (2,000 unit + 2,500 scenario tests validated against real bash)
- ~100% AI-generated code; most of the code reviewed by AI

Brief note on what this enabled: a fully cross-platform, sandboxed POSIX shell interpreter that AI agents can use to diagnose production systems, with a security model that operators can configure and audit.

### 7. What's Next (~150 words)

Brief forward look (draw from RFC future enhancements):
- More commands
- OS-level sandboxing layers (Landlock, App Sandbox)
- Integration into the Datadog Agent as an MCP tool exposed over PAR
- Potential for a remote shell UI in Fleet Automation

Close by returning to the through-line: the question of *trust*. We built a shell that gives AI agents real power with real constraints. We also built a development process that gives AI systems real autonomy with real guardrails. The two problems turned out to be the same problem.

---

## Tone Notes

- Confident but not boastful. "We learned X" > "we crushed it."
- Technical enough to be credible to engineers, accessible enough for engineering managers and AI-curious readers.
- Use concrete numbers when you have them. The stats are genuinely impressive—let them speak.
- Avoid marketing language ("game-changing," "next-generation"). Describe what happened.

---

## Key Sources

- `blog/Restricted Shell RFC.md` — motivation, design decisions, security layers, alternative analysis
- `blog/STATS.md` — the numbers
- Skills: `.claude/skills/implement-posix-command/SKILL.md` and `.claude/skills/review-fix-loop/SKILL.md` — the harness workflow detail
- Repository structure — concrete examples of builtins, tests, scenarios
