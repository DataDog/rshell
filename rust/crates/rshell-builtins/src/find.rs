//! `find` — walk a directory tree. Phase 4 baseline supports
//! `-name PATTERN` (glob), `-type f|d|l`, `-maxdepth N`, `-mindepth N`,
//! `-print`, `-print0`, and the implicit AND between predicates.

use rshell_interp::{Builtin, CallCtx};

pub struct Find;

impl Builtin for Find {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut paths: Vec<String> = Vec::new();
        let mut filters: Vec<Filter> = Vec::new();
        let mut print_zero = false;
        let mut explicit_print = false;
        let mut maxdepth = usize::MAX;
        let mut mindepth = 0;

        let mut i = 1;
        while i < ctx.args.len() {
            let a = ctx.args[i].as_slice();
            if a == b"--help" {
                let _ = ctx.stdout.write_all(b"Usage: find [PATH]... [-name PAT] [-type f|d|l] [-maxdepth N] [-mindepth N] [-print|-print0]\n");
                return 0;
            }
            if a.starts_with(b"-") {
                // It's a predicate.
                let key = a;
                let val = if needs_value(key) {
                    i += 1;
                    if i >= ctx.args.len() {
                        let _ = writeln!(
                            ctx.stderr,
                            "find: missing value for {}",
                            String::from_utf8_lossy(key)
                        );
                        return 1;
                    }
                    Some(ctx.args[i].as_slice())
                } else {
                    None
                };
                match key {
                    b"-name" => filters.push(Filter::Name(val.unwrap().to_vec())),
                    b"-iname" => filters.push(Filter::IName(val.unwrap().to_vec())),
                    b"-type" => filters.push(Filter::Type(*val.unwrap().first().unwrap_or(&b'f'))),
                    b"-maxdepth" => maxdepth = parse_uint(val.unwrap()),
                    b"-mindepth" => mindepth = parse_uint(val.unwrap()),
                    b"-print" => explicit_print = true,
                    b"-print0" => {
                        explicit_print = true;
                        print_zero = true;
                    }
                    _ => {}
                }
                i += 1;
                continue;
            }
            paths.push(String::from_utf8_lossy(a).into_owned());
            i += 1;
        }
        let _ = explicit_print;
        if paths.is_empty() {
            paths.push(".".into());
        }
        for root in &paths {
            walk(root, 0, maxdepth, mindepth, &filters, print_zero, ctx);
        }
        0
    }
}

enum Filter {
    Name(Vec<u8>),
    IName(Vec<u8>),
    Type(u8),
}

fn needs_value(key: &[u8]) -> bool {
    matches!(
        key,
        b"-name" | b"-iname" | b"-type" | b"-maxdepth" | b"-mindepth"
    )
}

fn parse_uint(s: &[u8]) -> usize {
    std::str::from_utf8(s).unwrap_or("0").parse().unwrap_or(0)
}

fn walk(
    path: &str,
    depth: usize,
    maxdepth: usize,
    mindepth: usize,
    filters: &[Filter],
    zero: bool,
    ctx: &mut CallCtx<'_>,
) {
    if depth > maxdepth {
        return;
    }
    let metadata = match std::fs::metadata(path) {
        Ok(m) => m,
        Err(_) => return,
    };
    if depth >= mindepth && matches_all(path, &metadata, filters) {
        let _ = ctx.stdout.write_all(path.as_bytes());
        let _ = ctx.stdout.write_all(if zero { b"\0" } else { b"\n" });
    }
    if metadata.is_dir()
        && let Ok(entries) = std::fs::read_dir(path)
    {
        let mut items: Vec<std::path::PathBuf> = entries.flatten().map(|e| e.path()).collect();
        items.sort();
        for ent in items {
            let p = ent.to_string_lossy().into_owned();
            walk(&p, depth + 1, maxdepth, mindepth, filters, zero, ctx);
        }
    }
}

fn matches_all(path: &str, metadata: &std::fs::Metadata, filters: &[Filter]) -> bool {
    let basename = path.rsplit('/').next().unwrap_or(path).as_bytes().to_vec();
    for f in filters {
        match f {
            Filter::Name(p) => {
                if !glob_match(&basename, p) {
                    return false;
                }
            }
            Filter::IName(p) => {
                let name_lower: Vec<u8> = basename.iter().map(|b| b.to_ascii_lowercase()).collect();
                let pat_lower: Vec<u8> = p.iter().map(|b| b.to_ascii_lowercase()).collect();
                if !glob_match(&name_lower, &pat_lower) {
                    return false;
                }
            }
            Filter::Type(t) => {
                let ft = metadata.file_type();
                let ok = match t {
                    b'f' => ft.is_file(),
                    b'd' => ft.is_dir(),
                    b'l' => ft.is_symlink(),
                    _ => true,
                };
                if !ok {
                    return false;
                }
            }
        }
    }
    true
}

fn glob_match(text: &[u8], pat: &[u8]) -> bool {
    fn rec(t: &[u8], p: &[u8]) -> bool {
        if p.is_empty() {
            return t.is_empty();
        }
        match p[0] {
            b'*' => {
                if rec(t, &p[1..]) {
                    return true;
                }
                if t.is_empty() {
                    return false;
                }
                rec(&t[1..], p)
            }
            b'?' => {
                if t.is_empty() {
                    return false;
                }
                rec(&t[1..], &p[1..])
            }
            c => {
                if t.is_empty() || t[0] != c {
                    return false;
                }
                rec(&t[1..], &p[1..])
            }
        }
    }
    rec(text, pat)
}
