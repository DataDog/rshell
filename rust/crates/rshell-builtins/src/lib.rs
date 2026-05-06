//! Builtin commands. One module per command. Phase 3 baseline ports the
//! handful needed by the interpreter smoke set; the rest land in Phase 4.

use rshell_interp::BuiltinRegistry;

pub mod cat;
pub mod echo;
pub mod exit;
pub mod false_;
pub mod pwd;
pub mod true_;

/// Register every builtin available in this crate. New crates should call
/// this once during runner construction; tests can construct a registry
/// piecemeal via the `Builtin` impls directly.
pub fn register_all(reg: &mut BuiltinRegistry) {
    reg.register("echo", echo::Echo);
    reg.register("true", true_::True);
    reg.register("false", false_::False);
    reg.register("pwd", pwd::Pwd);
    reg.register("cat", cat::Cat);
    reg.register("exit", exit::Exit);
    reg.register(":", true_::True);
}
