//! Builtin commands. One module per command.

use rshell_interp::BuiltinRegistry;

pub mod cat;
pub mod cut;
pub mod du;
pub mod echo;
pub mod exit;
pub mod false_;
pub mod find;
pub mod grep;
pub mod head;
pub mod help;
pub mod ip;
pub mod ls;
pub mod ping;
pub mod printf;
pub mod ps;
pub mod pwd;
pub mod read;
pub mod sed;
pub mod sort;
pub mod ss;
pub mod strings_cmd;
pub mod tail;
pub mod testcmd;
pub mod tr;
pub mod true_;
pub mod uname;
pub mod uniq;
pub mod wc;

/// Register every builtin available in this crate.
pub fn register_all(reg: &mut BuiltinRegistry) {
    reg.register("echo", echo::Echo);
    reg.register("true", true_::True);
    reg.register("false", false_::False);
    reg.register("pwd", pwd::Pwd);
    reg.register("cat", cat::Cat);
    reg.register("exit", exit::Exit);
    reg.register(":", true_::True);
    reg.register("printf", printf::Printf);
    reg.register("head", head::Head);
    reg.register("tail", tail::Tail);
    reg.register("wc", wc::Wc);
    reg.register("cut", cut::Cut);
    reg.register("sort", sort::Sort);
    reg.register("uniq", uniq::Uniq);
    reg.register("uname", uname::Uname);
    reg.register("help", help::Help);
    reg.register("test", testcmd::Test);
    reg.register("[", testcmd::Test);
    reg.register("tr", tr::Tr);
    reg.register("grep", grep::Grep);
    reg.register("sed", sed::Sed);
    reg.register("ls", ls::Ls);
    reg.register("find", find::Find);
    reg.register("du", du::Du);
    reg.register("strings", strings_cmd::Strings);
    reg.register("read", read::Read_);
    reg.register("ps", ps::Ps);
    reg.register("ip", ip::Ip);
    reg.register("ss", ss::Ss);
    reg.register("ping", ping::Ping);
}
