# **\[RFC\] Agent Restricted Shell (prev. Safe Shell)**

Author: [Alexandre Yang](mailto:alexandre.yang@datadoghq.com) [Matthew DeGuzman](mailto:matthew.deguzman@datadoghq.com) [Valeri Pliskin](mailto:valeri.pliskin@datadoghq.com)   
Date: Feb 23, 2026  
Status: **Ready for Review**

| ![People][image1] Reviewer | ![Dropdowns][image2] Status | ![No type][image3] Notes |
| :---- | :---- | :---- |
| [Noman Hamlani](mailto:noman.hamlani@datadoghq.com) | Not started |  |
| [Gabriel Plassard](mailto:gabriel.plassard@datadoghq.com) | Approved |  |
| [Igor Minin](mailto:igor.minin@datadoghq.com) | Approved |  |
| [Valeri Pliskin](mailto:valeri.pliskin@datadoghq.com) | Approved | Added a few comments. Also I am missing info about authorization layer \-\> can we just use PAR GRACE or will we need to create our own logic (prefer to avoid that).  |
| [Jules Macret](mailto:jules.macret@datadoghq.com) | In progress |  |
| [Vincent Boulineau](mailto:vincent.boulineau@datadoghq.com) | Not started |  |
| [Christophe Mourot](mailto:christophe.mourot@datadoghq.com) | Not started |  |
| [Travis Thieman](mailto:travis.thieman@datadoghq.com) | Approved |  |
| [Jules Denardou](mailto:jules.denardou@datadoghq.com) | Not started |  |
| Person | Not started |  |

Feel free to add your name to this review list.

[Overview](#overview)

[Proposed Solution: Agent Restricted Shell](#proposed-solution:-agent-restricted-shell)

[Restricted Shell compared to other solutions](#restricted-shell-compared-to-other-solutions)

[Restricted Shell Interpreter](#restricted-shell-interpreter)

[Restricted shell features and commands](#restricted-shell-commands)

[Performance](#performance)

[Multi-platform Support](#multi-platform-support)

[MCP Tool](#mcp-tool)

[FAQ](#heading=h.bfn4q49lm7wm)

[Security](#security-\(wip\))

[Restricted shell deployment context](#restricted-shell-deployment-context)

[Using Private Action Runner as Transport](#using-private-action-runner-as-transport)

[Deployment configuration](#deployment-configuration)

[Alternative Solutions](#alternative-solutions)

[Future Enhancements](#future-enhancements/ideas)

[Open Questions](#open-questions)

[Appendix](#appendix)

[Guardrails for running host commands](#guardrails-for-running-host-commands)

[How is this Agent Restricted Shell different from "restricted shell" discussed in MCP Mode Evaluation?](#how-is-this-agent-restricted-shell-different-from-"restricted-shell"-discussed-in-mcp-mode-evaluation?)

# **Overview** {#overview}

**Problem:**   
AI Agents require a flexible, yet secure, on-host execution environment to perform complex diagnostics on large local data volumes. Existing tools lack the necessary shell scripting features (e.g., piping, loops) that agents are trained on, while an unrestricted host shell poses an unacceptable security risk. The Agent Restricted Shell is proposed to bridge this gap, offering shell power with fine-grained control and security.

**Proposal:**  
This is a proposal to have an **Agent Restricted Shell** to allow AI Agent to interact with host resources.  
We will be able to provide an Agent Restricted Shell MCP tool that accepts a shell command/script as input and the output is what's returned by the shell script itself.  
This is possible by relying on [mvdan/sh](https://github.com/mvdan/sh) (golang) shell parser and by implementing our own custom interpreter ([working POC here](https://github.com/DataDog/datadog-agent/pull/46945)). Since we have full control on the interpreter implementation, we can make it safe by [allowing only specific shell features and commands](?tab=t.ju93j484bvp).

**Motivations:**

* ✅AI Agent are trained on POSIX commands  
* ✅AI Agent can use shell script (shell script is [turing complete](https://en.wikipedia.org/wiki/Turing_completeness))  
  * shell features ( \`|\` pipes, for loops, etc) and basic shell commands (grep, cat, find, etc)  
* ✅We have full control on the shell features & commands implementation  
  ([see pkg/shell/interp/runner.go in POC](https://github.com/DataDog/datadog-agent/pull/46758/changes#diff-ab5113f76aecf443f47426a696178ed55c306ff7f270cc1604b91a772cd386a0))  
* ✅Low effort to generate POSIX commands using AI Coding tools ([List of POSIX commands](https://en.wikipedia.org/wiki/List_of_POSIX_commands))

**Use cases:**  
Use case: AI Agent investigation requires scanning a large number of large files  
Investigating a large number of large files must be done locally and requires some kind of scripting/code ability to achieve it effectively. Where sending files to the backend is not viable.

Use case: AI Agent investigation requires complex scripted action  
Use of for loop and complex shell commands using \`|\` pipe.

POC Example:  
Example: "Search and summarise for all issues in /var/log/datadog/"  
Full multi-step AI Agent investigation here: [POC cmd+i session](https://docs.google.com/document/d/1MakeeyyxZF166oraRlRCEFAEm-vwGE4c7_W-m-ZoUkU/edit?pli=1&tab=t.490f8pdot90y)

```shell
LOGDIR="/host/var/log/datadog"

# Print the last few ERROR lines from every log file
find "$LOGDIR" -type f -name "*.log" | while read -r f; do
  echo "== $f =="
  grep "ERROR" "$f" | tail -n 5
  echo
done
```

**Prior Art and related docs:**

* [Datadog Agent MCP](https://docs.google.com/document/d/1uyPftvDz1jv_vxJ88RdG0H3lpgHyPppJNzDHXtjWFfY/edit)  
* [Page: MCP Mode Evaluation](https://datadoghq.atlassian.net/wiki/spaces/agent/pages/6036259682/MCP+Mode+Evaluation)  
* [https://github.com/scottopell/safe-shell](https://github.com/scottopell/safe-shell)

# **Proposed Solution: Agent Restricted Shell** {#proposed-solution:-agent-restricted-shell}

The Agent Restricted Shell approach will be implemented in Golang within the PAR process.

We will leverage the existing documentation on the safe-shell concept, potentially with minor adjustments, to inform this implementation.

The core of the Restricted Shell will be built using the [mvdan/sh](https://github.com/mvdan/sh) Golang shell parser. We will fork/develop a custom interpreter (a working [POC is available here](https://github.com/DataDog/datadog-agent/pull/46992)) that provides complete control over execution. This control allows us to ensure safety by [restricting execution to only specific shell features and commands](#restricted-shell-commands).

Furthermore, we plan to release an Agent Restricted Shell MCP tool. This utility will accept a shell command or script as input and return the output generated by the script's execution.

## Restricted Shell compared to other solutions {#restricted-shell-compared-to-other-solutions}

|   | Restricted ShellRecommended | Custom Tools/Actions | Pre-defined scripts | Restricted Shell with Sandbox |
| :---- | :---- | :---- | :---- | :---- |
| **AI Agent effectiveness** | ✅Easy for AI Agent to use the shell commands, LLMs are trained on POSIX shell commands | ⚠️Detailed description is needed to tell AI Agent how to use the tools | ⚠️Some description is needed to tell AI Agent how to use the tools | ⚠️Sandbox limitations can limit the AI Agent (more investigation needed) |
| **Flexibility / slice & dice data** | ✅Native support for piping, grep, tail, sort, cut, etc. enables powerful ad‑hoc data exploration | 🛑No native shell-like composition | 🛑 Very rigid | ✅Support shell scripting and potentially code execution |
| **Implementation effort** | ⚠️Medium effort to implement standard commands that are POSIX compliant, using AI coding tools. e.g. [grep and tail in this POC](https://github.com/DataDog/datadog-agent/pull/46758) were fully code by Claude Code. |  ⚠️ We will have custom implementation for each action (VS just "implementing" a standard commands) | ⚠️ Many commands could not be available on the host | ✅Effort relatively low (for linux) |
| **Control over execution environment** | ✅Full control over the shell implementation, we can restrict commands and options. | ✅ Full control over the implementation | ⚠️ Commands might not be available on host ⚠️ Commands version can differ | ⚠️ Commands might not be available on host⚠️ Commands version can differ |
| **Safety / sandboxing** | ✅Safe by gating correctly which shell features and commands are allowed. ✅By implementing tools as golang builtins, we can control file level access. ⚠️Extensive audit needed to ensure the safe-shell interpreter and commands are safe. ⚠️File level access control is enforced in code, less secure than "real" sandbox e.g. Landlock/VM | ✅ Safety guaranteed by the tool impl. | ✅ Assumed safe if the pre-defined command is allowed | ✅Safety based on Linux Landlock at Kernel level |
| **Platform Compatibility** | ✅Works anywhere where the Datadog Agent runs since the safe-shell interpreter is part of the Agent golang code. | ✅Works anywhere where the Datadog Agent runs | ✅Works anywhere where the Datadog Agent runs | ⚠️Linux only, requires Linux kernel 5.13+ MacOS and Windows with different setup |
| **Resource usage** | ✅Low | ✅Low | ✅Low | ✅Low |
| **Availability** | ✅Builtins are always available ⚠️Host commands might not be available | ⚠️Host commands might not be available | 🛑 Pre-defined scripts can be broken if the command is not installed on the host. | ⚠️Host commands might not be available |
| **Dev velocity for new commands** | ✅Low/medium, we only need to add the new command in DD Agent (and update the MCP/Skill description to mark it as supported) | ⚠️Will need a new custom action for each new command/tool | ✅Low/medium, we only need to add new pre-defined command in agent | ✅Low/medium, same as Restricted Shell without sandbox |
| **Notes** |  |  |  |  |

## Restricted Shell Interpreter {#restricted-shell-interpreter}

We intend to use [mvdan/sh](https://github.com/mvdan/sh) as shell parser, for the interpreter implementation, we have two options:

* **1/ Minimal Golang interpreter Recommended**  
  (based on [mvdan/sh/tree/master/interp](https://github.com/mvdan/sh/tree/master/interp), [POC \~300 lines](https://github.com/DataDog/datadog-agent/pull/47089/changes#diff-fd2126dcdb85fa2f144e011156aa6dbb2bfb222789d406235d31e4a539c44e6eR1))  
  * 🟢zero Dependencies  
  * 🟢security:  
    * full control on shell features  
    * will allow implementation of allow list in golang  
    * user / AI Agent cannot execute non allowed shell syntax/feature by design  
  * ⚠️we need to maintain the impl.  
* **2/ Add verifier for the syntax (AST)**  
  by relying on host shell (e.g. /bin/sh) or an embedded standard shell  
  * 🟢no need to implement the interpreter ourself  
  * ⚠️shell version/impl. can vary across (e.g. /bin/sh)  
  * 🛑less secure:  
    * due to reliance on an external binary that is critical (replacement of binary, TOCTOU attacks on allowed-paths)  
    * using syntax verifier is not 100% bullet proof to catch unintended usage (less safe compared to a custom interpreter impl), also some commands like awk/sed can be hard to verify due to internal DSL

**Shell Parser & Grammar validation:**

* 1/ Use [mvdan/sh](https://github.com/mvdan/sh) well maintained shell parser in Golang **Recommended**  
  * 🟢Low effort implementation, just need to import golang package  
  * 🟢Combined with "1/ Minimal Golang interpreter", we have good guarantee it won't execute non allowed shell features  
* 2/ Grammar-Based Validation Layer (Pre-Execution) comment  
  * Concept: Use formal grammar (ANTLR4) to filter LLM shell commands before execution: structural safety, not just verifications  
  * 🟢 Structural validation: Parse LLM output against restricted shell subset grammar before execution  
    * only grammatically valid commands proceed  
    * cannot bypass grammar constraints  
  * 🟢 Grammar \= explicit allowlist:  
    * Define "restricted shell subset" in formal grammar (.g4 file)  
    * Auditable: review grammar file, not code logic  
    * Compositional: complex commands validated structurally  
  * 🟢 Complementary:  
    * Works additively with interpreter option 1: If interpreter has gaps, grammar already rejected command  
  * 🟢 Fast feedback loop  
  * 🟢 Clear rejection semantics:  
    * Unparseable \= rejected with precise error location  
    * Can provide LLM feedback for retry with grammar hints  
  * ⚠️Requires grammar definition:  
    * Need to define "restricted shell subset" grammar upfront  
    * Grammar maintenance as requirements evolve

## Restricted Shell Commands {#restricted-shell-commands}

*Note: RFC review should be focused on shell features/command for v0 \- Dash*

|  | v0 \- Dash | Candidates for v1 (H2 2026, TBC) | Maybe later | Drop |
| :---- | :---- | :---- | :---- | :---- |
| **Shell features** | \- **command separators** (";" or "\\n") \- **logical operators** ("&&" "||") \- **pipe** "|" \- **for clause** \- **if clause** (if / elif / else) (tentative) \- **variable assignment** and  expansion ("key=value; echo $key") w/o subshell \- **allow /dev/null** e.g. cat file 2\>/dev/null (tentative) \- **Globbing**: \*, ?, \[abc\], \[a-z\], \[\!a\] \- **exit status of previous command** $? \- (tentative) | case clause while clause until clause | redirections \< redirection \<\< (used with EOF) compound commands brace\_group (?) Command Subs (...) and $(...) Process Substitution \<(...) and \>(...) $$ PID of current shell time \- for measuring command execution time  | compound\_list "&" (background job) redirections \> \>\> (write redirection) |
| **Commands** | Built-in commands: \- **echo** \- **true** \- **false** \- **break** (for loop) \- **continue** (for loop) \- **exit** (for loop, early exit) \- **test / \[** \- **cat**  Commands impl. as built-in: \- **ls** \- **tail** \- **head** \- **find** (excl: \-exec, \-ok) \- **grep** \- **wc** \- **sort** (excl: \-o) \- **uniq** \- **tr** (tentative) \- **sed** (tentative) \- ⚠️some sed commands have ability to execute subshell commands ([details](https://chatgpt.com/share/e/69a15741-52ec-8004-b654-20bc8fdff920)) \- **cut** (tentative) \- **strings** (tentative) \- **printf** (tentative) Note: only a subset of the safe options will be implemented. e.g. find \-exec \-ok, and sort \-o won't be included. | **ping** \- \-f may cause denial of service (Networks team scope) \- **awk** (tentative)  | pwd cd wait \- also useful but another denial of service possibility (a timeout could mitigate this) return \- only useful if we allow shell functions to be written **read** (tentative) \- reads a single line from standard input, or from the file descriptor fd **ps[https://github.com/shirou/gopsutil/](https://github.com/shirou/gopsutil/)[https://github.com/mitchellh/go-ps](https://github.com/mitchellh/go-ps) du df ip** data retrieval tools netstats ss ip config | fg bg exec builtin, command \- may bypass our implemented builtins trap alias unalias printf \- basic functionality covered by echo, extra features likely not necessary mapfile readarray source / . \- executes arbitrary scripts dirs \- pwd \+ cd are sufficient eval getopts shopt umask pushd popd type set shift unset |

Design choices for implementing safe-shell commands:

* **1/ re-implement the command in Golang Recommended**  
  * ⚠️we need to re-implement many tools  
  * 🟢full control over implementation  
  * 🟢allow level permission (wrapper go functions like os.Open())  
  * 🟢fulll platform compatibility (linux, macOS, windows)  
  * 🛑risk of implementing tools incorrectly (good harness can help)  
* **2/ use host commands by restricting commands and options**  
  * 🟢less code  
  * ⚠️need verification of each option  
  * ⚠️the command might be not available  
  * 🛑verifying options might not be secure:  
    * the host command might not behave as expected or corrupted  
    * the options verifier might not be able to catch all cases ([example](https://github.com/DataDog/datadog-agent/pull/46992#discussion_r2867814423))  
* **3/ ship commands with datadog-agent (e.g. [coreutils](https://github.com/uutils/coreutils))**  
  * 🟢better security, we can fully trust the commands  
  * ⚠️can increase datadog-agent footprint  
  * ⚠️extra agent build complexity  
  * 🛑we can't fully control what options are shipped with the embedded commands, meaning adding risk of running unintended options e.g. find \-exec  
  * ⚠️allow list verification need to be implement at command arguments level, which is less secure than 1/ where we can impl. file access by wrapping golang file access functions (e.g. os.Open())

Note: we are still exploring sandboxing methods that can influence the recommended option above (e.g. [Linux Landlock](https://docs.kernel.org/userspace-api/landlock.html), [agentfs](https://github.com/tursodatabase/agentfs))

## File allowlist (WIP)

\[We are still exploring best technical design to achieve File allowlist\]

A file allowlist will be implemented to only authorise reading specific files/folders.  
With all commands implemented as builtins, we can add a layer of verification around file access operation (e.g. read file).

Implementation details is still pending:

* The file allowlist can be implemented in Agent via datadog.yaml configuration.  
* The allowlist can be also passed from backend via PAR.

Allowlist vs Denylist:

* **Allowlist**  
  * 🟢safer, and will forbid by design all non allowlisted files  
  * ⚠️add friction for customers that required additional paths (beyond default allowed paths), as additional onboarding step.  
* **Denylist**  
  * 🟢less friction for customers  
  * 🔴less safe since we AI Agent will be able to access all files non tracked by the denylist. Denlylist is likely too risky.

Open Questions

* What is the default file allow list?  
* How will customers customize it?

## Performance {#performance}

Pending performance measurements.

Note: resource utilization ([@Pierre](https://dd.enterprise.slack.com/team/U04MM5J35TL) and [@Gabriel Plassard](https://dd.enterprise.slack.com/team/U05JRQLRXP0) can help [provide](https://dd.slack.com/archives/C0A9L3290GZ/p1771860608258489) the RSS for PAR in idle)

## Multi-platform support {#multi-platform-support}

The long term goal is to support all platforms (mac,windows,linux).

For v0, the goal is to support the linux/k8s environment at minimum.

## MCP Tool {#mcp-tool}

We will have an MCP tool for safe-shell (e.g. run\_safe\_shell) that will take a shell command/script as input and return the execution output of the command.

About how the AI Agent will know what commands and options are available:

* v0: manual MCP Tool description with available commands and options  
* future: command discovery mechanism

How to decide if a tool should be implemented:

* via safe-shell as builtin  
* via standalone MCP/PAR tool

Related doc: [Page: Datadog Agent MCP Tools](https://datadoghq.atlassian.net/wiki/spaces/agent/pages/6153732356/Datadog+Agent+MCP+Tools)

# **Security (WIP)** {#security-(wip)}

Related doc: [Agent MCP Tools: Security Review](https://docs.google.com/spreadsheets/d/1KxlpR8__hZPREsbrOh9UL920dqZDoXy4WD0W-N8Bz-0/edit?gid=0#gid=0)

The design choices in [Restricted Shell Interpreter](#restricted-shell-interpreter) and [Restricted shell features and commands](#restricted-shell-commands) are oriented toward optimising for security.

## Execution protection layers

* **Layer 1: Safe-shell interpreter protections** (Recommended for v0/dash)  
  * Protections  
    * **restricted shell features:** safe-shell interpreter only allow subset of restricted shell features  
    * **only runs builtins:** only runs builtins commands (incl. re-implemented commands like cat, ls, find, etc), it never runs host commands  
    * **no network operations**: no builtins commands have network access operations  
    * **file allowlist**: all commands wrap file operations to reject all operations on non allowed files/folders  
  * Accepted risks  
    * interpreter and commands implementation risk (security bugs)  
      \=\> increased audit/reviews can help mitigate this risk  
  * Motivation:  
    * Compatibility across linux version and macOS / Windows  
* **Layer 2: Sandboxing** (optional, can be implemented incrementally, per platform)  
  * Add sandbox layer per platform:  
    * [Linux: The SysProcAttr Approach or Landlock](https://docs.google.com/document/d/1id3qAbt-fdW7f9oYwa4UEqh0wIwAJChWeM05Hvt30DE/edit?tab=t.0#heading=h.fb15a8u75b9s)  
      * Landlock if possible \+ fallback on SysProcAttr  
      * Alternative: [Bubblewrap](https://github.com/containers/bubblewrap)  
    * [macOS: Sandbox App Sandbox](https://docs.google.com/document/d/1id3qAbt-fdW7f9oYwa4UEqh0wIwAJChWeM05Hvt30DE/edit?tab=t.0#heading=h.sdzppkhg44zm)  
      * Alternative: [Seatbelt framework](https://theapplewiki.com/wiki/Dev:Seatbelt)  
    * [Windows: Job Objects and SIDs](https://docs.google.com/document/d/1id3qAbt-fdW7f9oYwa4UEqh0wIwAJChWeM05Hvt30DE/edit?tab=t.0#heading=h.kvw5897xwulg)  
    * note: sandboxing in most cases it requires safe-shell to be a binary  
  * Motivation  
    * Protection for running host binaries  
    * Better file access protection  
  * Running host binaries will an option with sandboxing  
    * What are those binaries from?  
      * host shipped binaries  
      * coreutils  
* Additional optional protections  
  * hardlink protection ([protected-hardlinks](https://docs.kernel.org/admin-guide/sysctl/fs.html#protected-hardlinks))  
    * warn user if protected-hardlinks is not enabled as vulnerability  
    * agent health can report the vulnerability  
  * health check  
    * sandbox not available  
    * missing hardlink protection

## Safe-shell interpreter implementation risks and mitigations

See "Execution protection layers \> Layer 1" section.

Ensure that those safe-guards are implemented in safe-shell interpreter:

* Forbid following symlink for all commands  
* Forbid glob expansion  
* Applying resource limits  
  * max pipe chaining   
  * max recursion  
  * max search depth  
* Filename-as-flag injection (for f in \*; do head $f; done)  
* Mitigate infinite loop (for loop)  
* [Bypassing Bash Restrictions \- Rbash | VeryLazyTech](https://www.verylazytech.com/linux/bypassing-bash-restrictions-rbash)  
* Forbid forks / external command execution  
* \<TODO add more items\>

This can be used as hints (todo) for safe-shell implementation.

## Commands implementation risk and mitigations

We are going to implement a dozen of shell commands. Since those commands will be used in the safe-shell via arbitrary shell scripts, we need to ensure they are safe.

The commands must be protected against:

* unintended file access:  
  * file access wrapper must be used (see file allowlist)  
  * path traversal (cat ../../../../../../etc/passwd)  
  * symlink exploitation (cat /tmp/symlink\_to\_secret)  
* unbounded read  
  * denial of service via large output (cat /dev/zero)  
* unintended to sensitive info access  
  * proc filesystem information leak (cat /proc/self/environ)  
* [more items here](https://github.com/DataDog/datadog-agent/pull/47222/changes#diff-e1d17d7d3fa08838e07314ddb5302e9a9b04c2e52d5d8bcbb808eb459cb74c13)

To mitigate implementation risks, we can:

* **coding agent harness** to ensure the implementation is safe  
* **2+ reviewers** minimum for each command implementation

## Secret scrubbing

* The secret scrubbing from safe-shell output will be handled by   
  * **Agent scrubber**   
  * and in the backend using **SDS**.

## Open Questions

* user ability to create hard link for files it doesn't have access can be an attack vector  
  * there is a linux config for disabling that feature

# **Restricted shell deployment context** {#restricted-shell-deployment-context}

This section is meant to give some context about where and how the safe-shell will be used.

## Using Private Action Runner as Transport {#using-private-action-runner-as-transport}

Agent Restricted Shell will leverage PAR (Private Action Runner) as transport from Agent to Datadog backend, so that the Restricted Shell can be exposed as an MCP tool. 

* More info about PAR: [Page: POC - Integrating the PAR inside the datadog agent](https://datadoghq.atlassian.net/wiki/spaces/ACT/pages/5389714021/POC+-+Integrating+the+PAR+inside+the+datadog+agent)  
* More info about Agent MCP: [Datadog Agent MCP](https://docs.google.com/document/d/1uyPftvDz1jv_vxJ88RdG0H3lpgHyPppJNzDHXtjWFfY/edit)

This also means that the Restricted Shell will run inside the Private Action Runner process.

## Deployment configuration {#deployment-configuration}

v0 scope: k8s environment

Restricted shell runs inside the datadog node agent. The current test setup uses the [datadog operator](https://github.com/datadog/datadog-operator) with a local kubernetes cluster. The agent config contains annotations to enable PAR and allow for the custom actions such as the restricted shell.

```
metadata:
  name: datadog-agent
  annotations:
    agent.datadoghq.com/private-action-runner-enabled: "true"
    agent.datadoghq.com/private-action-runner-configdata: |
      private_action_runner:
        enabled: true
        self_enroll: true
        actions_allowlist:
          - com.datadoghq.ddagent.testConnection
          - com.datadoghq.ddagent.shell.runShell    # restricted shell action
```

The private action runner config mounts the root file system into `/host`.

```
spec:
  override:
    nodeAgent:
        private-action-runner:
          volumeMounts:
            - name: host-root
              mountPath: /host
              readOnly: true
```

Currently, PAR can read whatever it wants from the mounted host directory. However, we will want to restrict its permissions on what it can read — how we go about this is still an open ended question, we are leaning towards an allowlist of files and directories.

# **Alternative Solutions** {#alternative-solutions}

* Custom Actions \-- see details in "Restricted Shell compared to other solutions" section  
* Pre-defined scripts \-- see details in "Restricted Shell compared to other solutions" section  
* [https://blog.cloudflare.com/code-mode-mcp/](https://blog.cloudflare.com/code-mode-mcp/)  
  * Running Python/nodeJS code not practical within Agent environment due to sandbox requirement (resource usage, runtime requirements)  
* Use EBNF grammar to generate parser (e.g. [ANTLR4](https://github.com/antlr/antlr4)) to gate specific shell features and run directly via host shell/bash (no custom interpreter) \-- suggestion from [Chris Nader](mailto:chris.nader@datadoghq.com)

# **Future Enhancements/Ideas** {#future-enhancements/ideas}

* Ship **stripped** busybox/uutils to have a full control on the executables  
  * prevent $PATH shadowing attacks, e.g: ls can be replaced with a malicious tool  
* Add some sandbox virtual FS (example: [agentfs](https://github.com/tursodatabase/agentfs)) \- for intermediate files created in the restricted-shell scripts  
  * have full audit  
  * prevent Out-of-storage DOS  
* Add fingerprinting validation on the host binaries we execute (make sure the ls binary is actual ls binary), i don't know if checksum is feasible here, because of the versioning...  
* Enhance the verifier to analyze the script / AST form of the script, to prevent infinite loops or other "dangerous" behaviour  
* Supporting more commands (See [Shell features & commands](?tab=t.ju93j484bvp#heading=h.kx0r4re3i0hn))  
* "tool-to-discover-tools" idea, related post: [https://blog.cloudflare.com/code-mode-mcp/](https://blog.cloudflare.com/code-mode-mcp/)  
  * safe-shell.search  
  * safe-shell.execute  
* On Linux, we can add an additional layer of protection by using Landlock  
* Backend verification of valid scripts (possibly using BNF)  
* Restricted shell can be also used for:  
  * We can offer a "remote shell" GUI in-app for users to inspect their hosts. For example, when viewing Agent in Fleet Automation.  
  * Restricted Shell could be used outside of MCP tools. e.g. very precise on-demand or remediation actions triggered from UI.

# **Open Questions** {#open-questions}

* 

# **Appendix** {#appendix}

## Guardrails for running host commands {#guardrails-for-running-host-commands}

A verifier will be added to the safe-shell interpreter to check if the command and options are allowed.

The current [implementation](https://github.com/DataDog/datadog-agent/pull/46992) relies on the shell parser from [mvdan/sh](https://github.com/mvdan/sh).

* The script is parsed and rejected if a command/flag is not present in the allowlist  
* If the script is accepted, it is executed via `os.exec`

There are alternatives we are exploring that affect implementation, but not behavior

* [Chris Nader](mailto:chris.nader@datadoghq.com) proposed using an ANTLR4+EBNF grammar to generate a parser to gate specific shell features and commands as opposed to using the mvdan/sh parser  
* We are also looking into using the shell interpreter from mvdan/sh as it may give us more control over what is executed as opposed to `os.exec`

We recognize running host binaries carries its risks. For example, `ls` can be replaced with a malicious tool and we’ll execute it anyway because it’s in our allowlist. We have several options on how to mitigate this:

* ship **stripped** busybox/uutils to have a full control on the executables  
* add some sandbox virtual FS (example: [agentfs](https://github.com/tursodatabase/agentfs)) \- for intermediate files created in the restricted-shell scripts  
  * have full audit  
  * prevent Out-of-storage DOS  
* add fingerprinting validation on the host binaries we execute (ensure binaries are what we expect)

Allowlist example:

```go
var allowedCommands = map[string]map[string]bool{
	"echo": toSet("-n", "-e", "-E"),
	"pwd":  toSet("-L", "-P"),
	"cd":   toSet("-L", "-P"),
	"ls": toSet(
		"-l", "-a", "-A", "-R", "-r", "-t", "-S", "-h", "-d", "-1",
		"-F", "-p", "-i", "-s", "-n", "-g", "-o", "-G", "-T", "-U",
		"-c", "-u",
	),       [...]
}
```

Script rejection example:

```
$ ./agent shell --command "curl http://evil.com"
Error: script verification failed: verification failed: command "curl" is not allowed

$ ./agent shell --command "echo x | sed -n 's|.*|whoami|ep'"
Error: script verification failed: verification failed: sed 's///e' flag (execute replacement) is not allowed
```

 

## How is this Agent Restricted Shell different from "restricted shell" discussed in [MCP Mode Evaluation](https://datadoghq.atlassian.net/wiki/spaces/agent/pages/6036259682/MCP+Mode+Evaluation?atlOrigin=eyJpIjoiMmFmMjRkOTY3MWFiNGMyOGFiYWE4ZTY4NWZkNDgxMmQiLCJwIjoiY29uZmx1ZW5jZS1jaGF0cy1pbnQifQ)? {#how-is-this-agent-restricted-shell-different-from-"restricted-shell"-discussed-in-mcp-mode-evaluation?}

POC code: [https://github.com/scottopell/safe-shell](https://github.com/scottopell/safe-shell)

Differences:

* The Agent Restricted Shell here is using [mvdan/sh](https://github.com/mvdan/sh) parser and the interpreter is embedded as golang code VS sandbox environment for [scottopell/safe-shell](https://github.com/scottopell/safe-shell).  
* The Agent Restricted Shell here allow **full control of the shell interpreter** execution environment  
  * we can choose what shell builtin commands we expose  
  * we can choose what host commands are allowed  
  * we can choose what shell features are allowed  
  * we can wrap files read/write access for builtins commands to only allow specific folders/files  
* Cross platform compatibility

[image1]: <data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABQAAAAQCAMAAAAhxq8pAAADAFBMVEUAAABAQEBERkZFR0ZER0ZDR0VISEhFRUVERkVER0ZDR0dDRkNESERDRkZER0VESEhESEZCR0RDR0ZER0dERkZGRkZDRkRARUVASEhERkVFSEhDR0VER0ZGRkZDR0VFSEVFRUVFR0VER0YAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACxWeV0AAAAInRSTlMAEH/fz58gMO/vn1BAoL9AgHDfcIBQoDAgz2CQr19vYGBvGxXIWQAAAIJJREFUeF5jYKAIMMIYHFIMDE9/QthMUDFmqRevGKRhKqBASQhECEM4MJUM32EMIGCG0n/FvwhLMDyDcFiggp9YZBkYvkI5WAFMO6M04x9p9m9QDpiUZofKMTz5BRNUYnjG8gXE4BVl+Pwa5qTPP8BiDJ/vMfAywGznBTERAO54ZAAAT90XrfZ0HKwAAAAASUVORK5CYII=>

[image2]: <data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABQAAAAQCAYAAAAWGF8bAAAAx0lEQVR4Xu2TYRHCMAyFKwEJSEBCjyVpXIAEHIATJCBhEpCAhEkA0tEtTVcod/zku8ufvDR7fduc+/NTmHkNge6t1QU82x0TQHRMg8i4t7rGI266QJc0b3Xt7Gq1d3jvV69zQyZUn9QAMHg5K8vnpuTBtFNzXzEawj5rKL0AmE7pFku3KXrR8jPHeaRELy00m2NhuYIstT1hPB8OqoF9dKmDbQQD3ZZcTznIJ2S1GmlZ5k4DhENa3FrbDz+Bk5eTIqhVdFbJ8wG0lJX5M/zhmwAAAABJRU5ErkJggg==>

[image3]: <data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABQAAAAQAQMAAAAs1s1YAAAABlBMVEUAAABER0byc6G0AAAAAXRSTlMAQObYZgAAAB9JREFUeF5jYEAD9h8YmEA0MwOYZmSWWQjhs4H56BgAT4ECDeGaeV4AAAAASUVORK5CYII=>