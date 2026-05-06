//! `ip` — basic `ip route show` / `ip route get` implementation reading
//! `/proc/net/route` directly. Other subcommands print "not supported".

use rshell_interp::{Builtin, CallCtx};

pub struct Ip;

impl Builtin for Ip {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        if ctx.args.len() < 2 {
            let _ = ctx
                .stdout
                .write_all(b"Usage: ip OBJECT { COMMAND | help }\n");
            return 1;
        }
        let obj = ctx.args[1].as_slice();
        if obj == b"--help" || obj == b"help" {
            let _ = ctx
                .stdout
                .write_all(b"Usage: ip {route} [show|get TARGET]\n");
            return 0;
        }
        if obj == b"route" || obj == b"r" {
            return run_route(&ctx.args[2..], ctx);
        }
        let _ = writeln!(
            ctx.stderr,
            "ip: subcommand {} not supported",
            String::from_utf8_lossy(obj)
        );
        1
    }
}

fn run_route(args: &[bstr::BString], ctx: &mut CallCtx<'_>) -> i32 {
    if args.is_empty() || args[0].as_slice() == b"show" || args[0].as_slice() == b"list" {
        return show_routes(ctx);
    }
    if args[0].as_slice() == b"get" {
        if args.len() < 2 {
            let _ = ctx.stderr.write_all(b"ip route: get requires a target\n");
            return 1;
        }
        return get_route(&args[1], ctx);
    }
    let _ = writeln!(
        ctx.stderr,
        "ip route: subcommand {} not supported",
        String::from_utf8_lossy(args[0].as_slice())
    );
    1
}

#[cfg(target_os = "linux")]
fn show_routes(ctx: &mut CallCtx<'_>) -> i32 {
    let raw = match std::fs::read_to_string("/proc/net/route") {
        Ok(s) => s,
        Err(e) => {
            let _ = writeln!(ctx.stderr, "ip route: {e}");
            return 1;
        }
    };
    for (i, line) in raw.lines().enumerate() {
        if i == 0 {
            continue;
        }
        let cols: Vec<&str> = line.split_whitespace().collect();
        if cols.len() < 8 {
            continue;
        }
        let iface = cols[0];
        let dest = parse_hex_ip(cols[1]);
        let gw = parse_hex_ip(cols[2]);
        let mask = parse_hex_ip(cols[7]);
        let prefix = mask_to_prefix(cols[7]);
        if dest == "0.0.0.0" && mask == "0.0.0.0" {
            let _ = writeln!(ctx.stdout, "default via {gw} dev {iface}");
        } else {
            let _ = writeln!(ctx.stdout, "{dest}/{prefix} via {gw} dev {iface}");
        }
    }
    0
}

#[cfg(not(target_os = "linux"))]
fn show_routes(ctx: &mut CallCtx<'_>) -> i32 {
    let _ = ctx
        .stderr
        .write_all(b"ip route: not supported on this platform\n");
    1
}

#[cfg(target_os = "linux")]
fn get_route(target: &bstr::BString, ctx: &mut CallCtx<'_>) -> i32 {
    // Naive: re-print the default route prefixed with the target.
    let raw = match std::fs::read_to_string("/proc/net/route") {
        Ok(s) => s,
        Err(_) => return 1,
    };
    for (i, line) in raw.lines().enumerate() {
        if i == 0 {
            continue;
        }
        let cols: Vec<&str> = line.split_whitespace().collect();
        if cols.len() < 8 {
            continue;
        }
        if parse_hex_ip(cols[1]) == "0.0.0.0" {
            let _ = writeln!(
                ctx.stdout,
                "{} via {} dev {} src 0.0.0.0",
                String::from_utf8_lossy(target),
                parse_hex_ip(cols[2]),
                cols[0]
            );
            return 0;
        }
    }
    1
}

#[cfg(not(target_os = "linux"))]
fn get_route(_target: &bstr::BString, ctx: &mut CallCtx<'_>) -> i32 {
    let _ = ctx
        .stderr
        .write_all(b"ip route get: not supported on this platform\n");
    1
}

#[cfg(target_os = "linux")]
fn parse_hex_ip(hex: &str) -> String {
    let v = u32::from_str_radix(hex, 16).unwrap_or(0);
    let bytes = v.to_le_bytes();
    format!("{}.{}.{}.{}", bytes[0], bytes[1], bytes[2], bytes[3])
}

#[cfg(target_os = "linux")]
fn mask_to_prefix(hex: &str) -> u32 {
    let v = u32::from_str_radix(hex, 16).unwrap_or(0);
    v.swap_bytes().count_ones()
}
