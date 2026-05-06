//! `exit` — terminate the shell with a given exit code.
//!
//! Phase 3 baseline: returns the exit code; the runner uses it as the
//! script's last exit. True "exit out of the script entirely" semantics
//! (which would unwind across loops and functions) are deferred.

use rshell_interp::{Builtin, CallCtx};

pub struct Exit;

impl Builtin for Exit {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        if ctx.args.len() <= 1 {
            return ctx.env.last_exit;
        }
        match std::str::from_utf8(ctx.args[1].as_slice())
            .ok()
            .and_then(|s| s.parse::<i32>().ok())
        {
            Some(n) => n & 0xff,
            None => {
                let _ = writeln!(ctx.stderr, "exit: numeric argument required");
                2
            }
        }
    }
}
