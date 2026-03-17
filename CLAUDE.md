@AGENTS.md

## Bash Tool Rules

- **Never use `$()` or backtick command substitution in Bash tool calls.** These trigger unsandboxed prompts. Instead, run the inner command first in a separate Bash call, then use the literal result in the follow-up command.
