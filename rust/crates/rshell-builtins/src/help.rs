//! `help` — list registered builtins. Phase 4 baseline.

use rshell_interp::{Builtin, CallCtx};

pub struct Help;

impl Builtin for Help {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        // We don't have access to the runner's registry from a builtin, so
        // emit a static summary. Future iterations can plumb the registry
        // through CallCtx.
        let names = [
            ":", "cat", "cut", "du", "echo", "exit", "false", "find", "grep", "head", "help", "ip",
            "ls", "ping", "printf", "ps", "pwd", "sed", "sort", "ss", "strings", "tail", "test",
            "[", "tr", "true", "uname", "uniq", "wc",
        ];
        for n in names {
            let _ = writeln!(ctx.stdout, "{n}");
        }
        0
    }
}
