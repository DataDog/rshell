## Restricted Shell: Security Approach

Document Created: Mar 18, 2026  
Author: [Travis Thieman](mailto:travis@datadoghq.com) (Telemetry Collection & Operations)  
Status: Still writing, get outta here

## Overview

The Restricted Shell, as part of Agent MCP, allows us to offer LLMs a tool to run shell one-liners such as this on customer infrastructure:

```
tail -f /path/to/file.log | grep "error" | sed -E 's/^.{11}//'
```

Except this won’t run a real shell, or real `tail`, or real `grep`, or real `sed`, because each of those things has a fairly sizable list of potential security concerns attached to it. Instead, we have created “safe” versions of all four of those things and an additional 20 or so shell builtins and features. Our initial version took around 2 weeks to build and can be found in the [public rshell repository](https://github.com/DataDog/rshell) which as of this writing contains around 38k lines of Go code and over 2,400 YAML-based test scenarios.

This document serves as a brief overview of how we approached (vibe) coding all of this, how we are thinking about safety and security, and where we are relying on AI and humans as part of developing these tools.

## What is Safe? Our Security Model

Did you know that common POSIX tool implementations have these safety-related issues?

* `find` and `sed` can both execute external binaries  
* `sort` can write to the filesystem  
* The default regex engine used by `grep` can trivially DoS your machine  
* `tail` sounds pretty easy until you greedily allocate buffers for `tail -n 9999999999999999` and immediately OOM yourself  
* Getting any of these tools to honor a filesystem access allowlist is pretty tough especially when you consider cross-platform and ancient Linux kernel version (RHEL still on 3.10\!) support  
* Shells can obviously do all sorts of nefarious things

The implementations for our shell interpreter and the builtin commands offered by it all adhere to the following constraints, enforced primarily through deterministic import allowlists:

* No writes to any filesystem  
* No deletes to any filesystem  
* No external binaries may be executed. Anything that looks like an execution is actually calling an rshell builtin implementation within our Go process.  
* Arbitrary, user-controlled reads to the filesystem are gated by a provided list of `allowed_paths`  
  * We allow selected hardcoded reads to the filesystem, mostly to `/proc` for our `ps` implementation. These paths cannot be altered by user input.  
* No network access  
  * There’s one exception here for the `ping` command which can only send ICMP Echo and does not allow data exfiltration

Additionally, 

## Development Approach and Usage of AI Tools

## Deterministic Controls and Human Reviews

