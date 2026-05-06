use rshell_interp::{Builtin, CallCtx};

pub struct False;

impl Builtin for False {
    fn run(&self, _: &mut CallCtx<'_>) -> i32 {
        1
    }
}
