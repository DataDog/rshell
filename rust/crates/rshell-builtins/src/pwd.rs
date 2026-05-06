//! `pwd` — print the working directory.

use rshell_interp::{Builtin, CallCtx};

pub struct Pwd;

impl Builtin for Pwd {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        // Phase 3 baseline ignores -L/-P (logical vs physical) and just
        // emits the runner's tracked cwd.
        let s = ctx.cwd.to_string_lossy();
        if let Err(e) = writeln!(ctx.stdout, "{s}") {
            let _ = writeln!(ctx.stderr, "pwd: {e}");
            return 1;
        }
        0
    }
}
