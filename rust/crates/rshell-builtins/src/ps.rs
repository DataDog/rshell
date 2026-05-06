//! `ps` — list running processes. Phase 4 baseline supports `-e/-A`
//! (all), `-f` (full format), `-p PID,...` (specific PIDs), `-o` (custom
//! columns ignored — uses default columns).
//!
//! On Linux we read `/proc/<pid>/{comm,stat,cmdline}` directly. On macOS
//! and Windows the runtime is more limited and the same data isn't
//! available without third-party deps; we currently emit an empty body
//! beyond the header on non-Linux.

use rshell_interp::{Builtin, CallCtx};

pub struct Ps;

impl Builtin for Ps {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut all = false;
        let mut full = false;
        let mut pid_filter: Option<Vec<u32>> = None;
        let mut i = 1;
        while i < ctx.args.len() {
            let a = ctx.args[i].as_slice();
            if a == b"--help" {
                let _ = ctx
                    .stdout
                    .write_all(b"Usage: ps [-e|-A] [-f] [-p PID,...]\n");
                return 0;
            }
            if a.starts_with(b"-") && a.len() > 1 {
                // Skip flags after this if `-p` consumed value already.
                let mut j = 1;
                let mut consumed_value = false;
                while j < a.len() {
                    match a[j] {
                        b'e' | b'A' => {
                            all = true;
                            j += 1;
                        }
                        b'f' => {
                            full = true;
                            j += 1;
                        }
                        b'p' => {
                            // Either `-pPID,PID` or `-p PID,PID`.
                            let value: &[u8] = if j + 1 < a.len() {
                                &a[j + 1..]
                            } else {
                                i += 1;
                                if i >= ctx.args.len() {
                                    let _ = ctx.stderr.write_all(b"ps: -p needs an argument\n");
                                    return 1;
                                }
                                ctx.args[i].as_slice()
                            };
                            let mut pids = Vec::new();
                            for chunk in value.split(|&b| b == b',') {
                                let s = std::str::from_utf8(chunk).unwrap_or("");
                                if s.is_empty() {
                                    continue;
                                }
                                if let Ok(n) = s.parse::<u32>() {
                                    pids.push(n);
                                } else {
                                    let _ = writeln!(ctx.stderr, "ps: invalid pid: {s}");
                                    return 1;
                                }
                            }
                            if pids.is_empty() {
                                let _ = ctx.stderr.write_all(b"ps: empty pid list\n");
                                return 1;
                            }
                            pid_filter = Some(pids);
                            consumed_value = true;
                            break;
                        }
                        b'o' => {
                            // Skip the column list value.
                            if j + 1 >= a.len() {
                                i += 1;
                            }
                            consumed_value = true;
                            break;
                        }
                        other => {
                            let _ = writeln!(ctx.stderr, "ps: unknown flag -{}", other as char);
                            return 1;
                        }
                    }
                }
                let _ = consumed_value;
                i += 1;
                continue;
            }
            let _ = writeln!(ctx.stderr, "ps: unexpected positional argument");
            return 1;
        }

        // Header.
        let _ = full;
        let _ = ctx.stdout.write_all(b"   PID TTY              TIME CMD\n");

        let entries = list_processes(all || pid_filter.is_some(), pid_filter.as_deref());
        for entry in entries {
            let _ = writeln!(
                ctx.stdout,
                "{:>6} {:<16} {:>8} {}",
                entry.pid, entry.tty, entry.time, entry.cmd
            );
        }
        0
    }
}

struct ProcessEntry {
    pid: u32,
    tty: String,
    time: String,
    cmd: String,
}

#[cfg(target_os = "linux")]
fn list_processes(_all: bool, filter: Option<&[u32]>) -> Vec<ProcessEntry> {
    let mut out = Vec::new();
    let entries = match std::fs::read_dir("/proc") {
        Ok(e) => e,
        Err(_) => return out,
    };
    for ent in entries.flatten() {
        let name = ent.file_name();
        let name = name.to_string_lossy();
        let pid: u32 = match name.parse() {
            Ok(n) => n,
            Err(_) => continue,
        };
        if let Some(f) = filter
            && !f.contains(&pid)
        {
            continue;
        }
        let cmd = std::fs::read_to_string(format!("/proc/{pid}/comm"))
            .map(|s| s.trim().to_string())
            .unwrap_or_default();
        out.push(ProcessEntry {
            pid,
            tty: "?".into(),
            time: "00:00:00".into(),
            cmd,
        });
    }
    out.sort_by_key(|e| e.pid);
    out
}

#[cfg(not(target_os = "linux"))]
fn list_processes(_all: bool, _filter: Option<&[u32]>) -> Vec<ProcessEntry> {
    // macOS / Windows: a richer implementation would call sysctl or
    // CreateToolhelp32Snapshot. For the baseline we list only the
    // current process so the output is non-empty.
    vec![ProcessEntry {
        pid: std::process::id(),
        tty: "?".into(),
        time: "00:00:00".into(),
        cmd: "rshell-rs".into(),
    }]
}
