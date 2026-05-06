//! `ping` — Phase 4 baseline implements the *validation surface* of
//! the Go rshell `ping` (the corpus mostly tests rejection paths). We
//! validate flags and host arguments and reject unicast-broadcast,
//! multicast, IPv6/IPv4 mismatch, and unsupported flags. Actual ICMP
//! emission is not implemented (returns 1 with a message).

use std::net::IpAddr;

use rshell_interp::{Builtin, CallCtx};

pub struct Ping;

impl Builtin for Ping {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut count: Option<u32> = None;
        let mut interval: Option<f64> = None;
        let mut deadline: Option<f64> = None;
        let mut force_v4 = false;
        let mut force_v6 = false;
        let mut quiet = false;
        let mut hosts: Vec<&[u8]> = Vec::new();
        let mut i = 1;
        while i < ctx.args.len() {
            let a = ctx.args[i].as_slice();
            if a == b"-h" || a == b"--help" {
                let _ = ctx.stdout.write_all(usage());
                return 0;
            }
            match a {
                b"-4" => {
                    force_v4 = true;
                    i += 1;
                    continue;
                }
                b"-6" => {
                    force_v6 = true;
                    i += 1;
                    continue;
                }
                b"-q" => {
                    quiet = true;
                    i += 1;
                    continue;
                }
                b"-c" => {
                    i += 1;
                    if i >= ctx.args.len() {
                        let _ = ctx.stderr.write_all(b"ping: -c requires an argument\n");
                        return 1;
                    }
                    let v = std::str::from_utf8(ctx.args[i].as_slice())
                        .ok()
                        .and_then(|s| s.parse::<u32>().ok());
                    match v {
                        Some(n) if n > 0 && n <= 100 => count = Some(n),
                        Some(0) => {
                            let _ = ctx.stderr.write_all(b"ping: -c must be > 0\n");
                            return 1;
                        }
                        Some(n) => {
                            let _ = writeln!(ctx.stderr, "ping: -c clamped from {n} to 100");
                            count = Some(100);
                        }
                        None => {
                            let _ = ctx.stderr.write_all(b"ping: -c invalid\n");
                            return 1;
                        }
                    }
                    i += 1;
                    continue;
                }
                b"-i" => {
                    i += 1;
                    if i >= ctx.args.len() {
                        let _ = ctx.stderr.write_all(b"ping: -i requires an argument\n");
                        return 1;
                    }
                    let s = std::str::from_utf8(ctx.args[i].as_slice()).unwrap_or("");
                    let v: f64 = match s.parse() {
                        Ok(v) => v,
                        Err(_) => {
                            let _ = writeln!(ctx.stderr, "ping: -i invalid: {s}");
                            return 1;
                        }
                    };
                    if v < 0.0 {
                        let _ = ctx.stderr.write_all(b"ping: -i must be >= 0\n");
                        return 1;
                    }
                    if v < 0.2 {
                        let _ = writeln!(ctx.stderr, "ping: -i clamped from {v} to 0.2");
                        interval = Some(0.2);
                    } else if v > 60.0 {
                        let _ = writeln!(ctx.stderr, "ping: -i clamped from {v} to 60");
                        interval = Some(60.0);
                    } else {
                        interval = Some(v);
                    }
                    i += 1;
                    continue;
                }
                b"-W" => {
                    i += 1;
                    if i >= ctx.args.len() {
                        let _ = ctx.stderr.write_all(b"ping: -W requires an argument\n");
                        return 1;
                    }
                    let s = std::str::from_utf8(ctx.args[i].as_slice()).unwrap_or("");
                    let v: f64 = match s.parse() {
                        Ok(v) => v,
                        Err(_) => {
                            let _ = writeln!(ctx.stderr, "ping: -W invalid: {s}");
                            return 1;
                        }
                    };
                    if v < 0.1 {
                        let _ = writeln!(ctx.stderr, "ping: -W clamped from {v} to 0.1");
                        deadline = Some(0.1);
                    } else if v > 60.0 {
                        let _ = writeln!(ctx.stderr, "ping: -W clamped from {v} to 60");
                        deadline = Some(60.0);
                    } else {
                        deadline = Some(v);
                    }
                    i += 1;
                    continue;
                }
                b"-b" | b"-f" | b"-I" | b"-p" | b"-R" | b"-s" => {
                    let _ = writeln!(
                        ctx.stderr,
                        "ping: option {} is not allowed",
                        String::from_utf8_lossy(a)
                    );
                    return 1;
                }
                _ => {}
            }
            if a.starts_with(b"-") && a.len() > 1 {
                let _ = writeln!(
                    ctx.stderr,
                    "ping: unknown flag {}",
                    String::from_utf8_lossy(a)
                );
                return 1;
            }
            hosts.push(a);
            i += 1;
        }
        if hosts.len() != 1 {
            let _ = ctx.stderr.write_all(if hosts.is_empty() {
                b"ping: missing host argument\nUsage: ping [options] host\n"
            } else {
                b"ping: too many host arguments\n"
            });
            return 1;
        }
        if force_v4 && force_v6 {
            let _ = ctx
                .stderr
                .write_all(b"ping: -4 and -6 are mutually exclusive\n");
            return 1;
        }

        let host = std::str::from_utf8(hosts[0]).unwrap_or("");
        let parsed = host.parse::<IpAddr>();
        if let Ok(ip) = parsed {
            // IPv4/IPv6 mismatch.
            if force_v4 && ip.is_ipv6() {
                let _ = ctx.stderr.write_all(b"ping: -4 with IPv6 literal\n");
                return 1;
            }
            if force_v6 && ip.is_ipv4() {
                let _ = ctx.stderr.write_all(b"ping: -6 with IPv4 literal\n");
                return 1;
            }
            // Reject special-purpose addresses.
            if ip.is_unspecified() {
                let _ = writeln!(ctx.stderr, "ping: cannot ping unspecified address");
                return 1;
            }
            if ip.is_multicast() {
                let _ = writeln!(ctx.stderr, "ping: cannot ping multicast address");
                return 1;
            }
            if let IpAddr::V4(v4) = ip {
                let octets = v4.octets();
                if octets[3] == 255 {
                    let _ = writeln!(ctx.stderr, "ping: cannot ping broadcast address");
                    return 1;
                }
                if octets == [255, 255, 255, 255] {
                    let _ = writeln!(ctx.stderr, "ping: cannot ping limited broadcast");
                    return 1;
                }
            }
        }

        let ic = count.unwrap_or(4);
        let i_ival = interval.unwrap_or(1.0);
        let _ = deadline;
        let _ = quiet;
        // Cumulative time check (corpus exercises this).
        let total = (ic as f64) * i_ival;
        if total > 120.0 {
            let _ = writeln!(
                ctx.stderr,
                "ping: total run time {total:.1}s exceeds 120s cap"
            );
        }
        let _ = writeln!(
            ctx.stderr,
            "ping: ICMP not implemented in this build; would have pinged {host}"
        );
        1
    }
}

fn usage() -> &'static [u8] {
    b"Usage: ping [-4|-6] [-c count] [-i interval] [-W timeout] [-q] host\n"
}
