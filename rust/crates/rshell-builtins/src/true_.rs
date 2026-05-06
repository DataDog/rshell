use rshell_interp::{Builtin, CallCtx};

pub struct True;

impl Builtin for True {
    fn run(&self, _: &mut CallCtx<'_>) -> i32 {
        0
    }
}
