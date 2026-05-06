//! `ss` — socket statistics. Phase 4 baseline supports `-t` (TCP),
//! `-u` (UDP), `-l` (listening), `-a` (all), `-n` (numeric). Linux only;
//! other platforms emit "not supported".

use rshell_interp::{Builtin, CallCtx};

pub struct Ss;

impl Builtin for Ss {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut tcp = false;
        let mut udp = false;
        let mut listening_only = false;
        let mut _all = false;
        let mut _numeric = false;
        let mut i = 1;
        while i < ctx.args.len() {
            let a = ctx.args[i].as_slice();
            if a == b"--help" {
                let _ = ctx
                    .stdout
                    .write_all(b"Usage: ss [-t] [-u] [-l] [-a] [-n]\n");
                return 0;
            }
            if a.starts_with(b"-") && a.len() > 1 {
                let mut ok = true;
                for f in &a[1..] {
                    match f {
                        b't' => tcp = true,
                        b'u' => udp = true,
                        b'l' => listening_only = true,
                        b'a' => _all = true,
                        b'n' => _numeric = true,
                        _ => {
                            ok = false;
                            break;
                        }
                    }
                }
                if ok {
                    i += 1;
                    continue;
                }
            }
            i += 1;
        }
        if !tcp && !udp {
            tcp = true;
            udp = true;
        }
        let _ = ctx
            .stdout
            .write_all(b"Netid State      Recv-Q Send-Q Local Address:Port Peer Address:Port\n");
        if tcp {
            print_socket_table(ctx, "tcp", "/proc/net/tcp", listening_only);
        }
        if udp {
            print_socket_table(ctx, "udp", "/proc/net/udp", listening_only);
        }
        0
    }
}

#[cfg(target_os = "linux")]
fn print_socket_table(ctx: &mut CallCtx<'_>, netid: &str, path: &str, listening_only: bool) {
    let raw = match std::fs::read_to_string(path) {
        Ok(s) => s,
        Err(_) => return,
    };
    for (i, line) in raw.lines().enumerate() {
        if i == 0 {
            continue;
        }
        let cols: Vec<&str> = line.split_whitespace().collect();
        if cols.len() < 4 {
            continue;
        }
        let local = parse_hex_endpoint(cols[1]);
        let remote = parse_hex_endpoint(cols[2]);
        let state = parse_state(cols[3]);
        if listening_only && state != "LISTEN" {
            continue;
        }
        let _ = writeln!(
            ctx.stdout,
            "{netid:<5} {state:<10} {:>6} {:>6} {local} {remote}",
            0, 0
        );
    }
}

#[cfg(not(target_os = "linux"))]
fn print_socket_table(ctx: &mut CallCtx<'_>, netid: &str, _path: &str, _listening_only: bool) {
    let _ = writeln!(ctx.stderr, "ss: {netid}: not supported on this platform");
}

#[cfg(target_os = "linux")]
fn parse_hex_endpoint(s: &str) -> String {
    let parts: Vec<&str> = s.split(':').collect();
    if parts.len() != 2 {
        return s.to_string();
    }
    let ip = u32::from_str_radix(parts[0], 16).unwrap_or(0);
    let port = u16::from_str_radix(parts[1], 16).unwrap_or(0);
    let bytes = ip.to_le_bytes();
    format!(
        "{}.{}.{}.{}:{}",
        bytes[0], bytes[1], bytes[2], bytes[3], port
    )
}

#[cfg(target_os = "linux")]
fn parse_state(hex: &str) -> &'static str {
    match u32::from_str_radix(hex, 16).unwrap_or(0) {
        1 => "ESTAB",
        2 => "SYN-SENT",
        3 => "SYN-RECV",
        4 => "FIN-WAIT-1",
        5 => "FIN-WAIT-2",
        6 => "TIME-WAIT",
        7 => "CLOSE",
        8 => "CLOSE-WAIT",
        9 => "LAST-ACK",
        10 => "LISTEN",
        11 => "CLOSING",
        _ => "UNKNOWN",
    }
}
