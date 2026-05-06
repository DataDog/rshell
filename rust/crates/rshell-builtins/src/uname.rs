//! `uname` — print system information. Phase 4 baseline supports `-s`,
//! `-r`, `-m`, `-a`, `-n`, `-o`, `-v`. Defaults to `-s`.

use rshell_interp::{Builtin, CallCtx};

pub struct Uname;

impl Builtin for Uname {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut want = Want::default();
        let mut idx = 1;
        while idx < ctx.args.len() {
            let a = ctx.args[idx].as_slice();
            if a == b"--help" {
                let _ = ctx.stdout.write_all(b"Usage: uname [-asnrvmo]\n");
                return 0;
            }
            if a.starts_with(b"-") && a.len() > 1 {
                for f in &a[1..] {
                    match f {
                        b'a' => {
                            want.kernel = true;
                            want.node = true;
                            want.release = true;
                            want.version = true;
                            want.machine = true;
                            want.os = true;
                        }
                        b's' => want.kernel = true,
                        b'n' => want.node = true,
                        b'r' => want.release = true,
                        b'v' => want.version = true,
                        b'm' => want.machine = true,
                        b'o' => want.os = true,
                        _ => {
                            let _ = writeln!(ctx.stderr, "uname: unknown flag -{}", *f as char);
                            return 1;
                        }
                    }
                }
                idx += 1;
                continue;
            }
            let _ = writeln!(ctx.stderr, "uname: unexpected argument");
            return 1;
        }
        if !want.any() {
            want.kernel = true;
        }
        let info = SysInfo::detect();
        let mut parts = Vec::new();
        if want.kernel { parts.push(info.kernel.clone()); }
        if want.node { parts.push(info.node.clone()); }
        if want.release { parts.push(info.release.clone()); }
        if want.version { parts.push(info.version.clone()); }
        if want.machine { parts.push(info.machine.clone()); }
        if want.os { parts.push(info.os.clone()); }
        let _ = writeln!(ctx.stdout, "{}", parts.join(" "));
        0
    }
}

#[derive(Default)]
struct Want {
    kernel: bool,
    node: bool,
    release: bool,
    version: bool,
    machine: bool,
    os: bool,
}

impl Want {
    fn any(&self) -> bool {
        self.kernel || self.node || self.release || self.version || self.machine || self.os
    }
}

struct SysInfo {
    kernel: String,
    node: String,
    release: String,
    version: String,
    machine: String,
    os: String,
}

impl SysInfo {
    fn detect() -> Self {
        let kernel = std::env::consts::OS.to_string();
        let kernel = match kernel.as_str() {
            "linux" => "Linux".to_string(),
            "macos" => "Darwin".to_string(),
            "windows" => "Windows".to_string(),
            other => other.to_string(),
        };
        let node = "rshell".to_string();
        let release = "0".to_string();
        let version = "rshell".to_string();
        let machine = std::env::consts::ARCH.to_string();
        let os = match std::env::consts::OS {
            "linux" => "GNU/Linux".to_string(),
            "macos" => "Darwin".to_string(),
            "windows" => "Windows".to_string(),
            o => o.to_string(),
        };
        Self { kernel, node, release, version, machine, os }
    }
}
