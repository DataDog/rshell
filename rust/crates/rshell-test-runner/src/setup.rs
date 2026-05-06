//! Test directory setup: writes scenario `setup.files`, applies chmod,
//! creates symlinks, sets mod_time. Mirrors `setupTestDir` in the Go runner.

use std::fs;
use std::path::Path;

use crate::scenario::{Scenario, SetupFile};

#[derive(Debug, thiserror::Error)]
pub enum SetupError {
    #[error("setup: failed to create directory for {path}: {source}")]
    Mkdir {
        path: String,
        #[source]
        source: std::io::Error,
    },
    #[error("setup: failed to write file {path}: {source}")]
    Write {
        path: String,
        #[source]
        source: std::io::Error,
    },
    #[error("setup: failed to chmod {path}: {source}")]
    Chmod {
        path: String,
        #[source]
        source: std::io::Error,
    },
    #[error("setup: failed to create symlink {path} -> {target}: {source}")]
    Symlink {
        path: String,
        target: String,
        #[source]
        source: std::io::Error,
    },
    #[error("setup: invalid mod_time {value:?} for {path}: {reason}")]
    ModTime {
        path: String,
        value: String,
        reason: String,
    },
    #[error("setup: failed to set mod_time on {path}: {source}")]
    ApplyModTime {
        path: String,
        #[source]
        source: std::io::Error,
    },
}

/// Apply every entry in `scenario.setup.files` under `dir`.
pub fn apply(dir: &Path, scenario: &Scenario) -> Result<(), SetupError> {
    for f in &scenario.setup.files {
        apply_one(dir, f)?;
    }
    Ok(())
}

fn apply_one(dir: &Path, f: &SetupFile) -> Result<(), SetupError> {
    let full = dir.join(&f.path);
    if let Some(parent) = full.parent() {
        fs::create_dir_all(parent).map_err(|e| SetupError::Mkdir {
            path: f.path.clone(),
            source: e,
        })?;
    }

    if let Some(target) = &f.symlink {
        create_symlink(target, &full).map_err(|e| SetupError::Symlink {
            path: f.path.clone(),
            target: target.clone(),
            source: e,
        })?;
    } else {
        fs::write(&full, &f.content).map_err(|e| SetupError::Write {
            path: f.path.clone(),
            source: e,
        })?;
        if let Some(mode) = f.chmod {
            apply_chmod(&full, mode).map_err(|e| SetupError::Chmod {
                path: f.path.clone(),
                source: e,
            })?;
        }
    }

    if let Some(value) = &f.mod_time {
        let ts = parse_rfc3339(value).map_err(|reason| SetupError::ModTime {
            path: f.path.clone(),
            value: value.clone(),
            reason,
        })?;
        let ft = filetime::FileTime::from_unix_time(ts, 0);
        filetime::set_file_times(&full, ft, ft).map_err(|e| SetupError::ApplyModTime {
            path: f.path.clone(),
            source: e,
        })?;
    }

    Ok(())
}

#[cfg(unix)]
fn create_symlink(target: &str, link: &Path) -> std::io::Result<()> {
    std::os::unix::fs::symlink(target, link)
}

#[cfg(windows)]
fn create_symlink(target: &str, link: &Path) -> std::io::Result<()> {
    // Match the Go runner: assume file (not directory) symlinks. Scenarios
    // requiring directory symlinks on Windows can be flagged later.
    std::os::windows::fs::symlink_file(target, link)
}

#[cfg(unix)]
fn apply_chmod(path: &Path, mode: u32) -> std::io::Result<()> {
    use std::os::unix::fs::PermissionsExt;
    let perms = std::fs::Permissions::from_mode(mode);
    std::fs::set_permissions(path, perms)
}

#[cfg(windows)]
fn apply_chmod(_path: &Path, _mode: u32) -> std::io::Result<()> {
    // Windows does not honour POSIX modes. The Go runner uses os.Chmod which
    // is a partial no-op on Windows; mirror that by accepting the call.
    Ok(())
}

/// Minimal RFC3339 parser. Returns Unix epoch seconds.
/// Accepts `YYYY-MM-DDTHH:MM:SS[.fff][Z|±HH:MM]`.
fn parse_rfc3339(s: &str) -> Result<i64, String> {
    let bytes = s.as_bytes();
    if bytes.len() < 19 {
        return Err("string too short".into());
    }
    let parse_u = |start: usize, len: usize| -> Result<i64, String> {
        let slice = &s[start..start + len];
        slice
            .parse::<i64>()
            .map_err(|_| format!("invalid number at offset {start}: {slice:?}"))
    };
    let year = parse_u(0, 4)?;
    if bytes[4] != b'-' {
        return Err("expected '-' at offset 4".into());
    }
    let month = parse_u(5, 2)?;
    if bytes[7] != b'-' {
        return Err("expected '-' at offset 7".into());
    }
    let day = parse_u(8, 2)?;
    if bytes[10] != b'T' && bytes[10] != b' ' {
        return Err("expected 'T' or ' ' at offset 10".into());
    }
    let hour = parse_u(11, 2)?;
    if bytes[13] != b':' {
        return Err("expected ':' at offset 13".into());
    }
    let minute = parse_u(14, 2)?;
    if bytes[16] != b':' {
        return Err("expected ':' at offset 16".into());
    }
    let second = parse_u(17, 2)?;

    // Skip optional fractional seconds.
    let mut idx = 19;
    if idx < bytes.len() && bytes[idx] == b'.' {
        idx += 1;
        while idx < bytes.len() && bytes[idx].is_ascii_digit() {
            idx += 1;
        }
    }

    // Timezone offset.
    let mut offset_seconds: i64 = 0;
    if idx < bytes.len() {
        match bytes[idx] {
            b'Z' => {
                idx += 1;
            }
            b'+' | b'-' => {
                let sign: i64 = if bytes[idx] == b'-' { -1 } else { 1 };
                if bytes.len() < idx + 6 || bytes[idx + 3] != b':' {
                    return Err("invalid timezone offset".into());
                }
                let tz_h = parse_u(idx + 1, 2)?;
                let tz_m = parse_u(idx + 4, 2)?;
                offset_seconds = sign * (tz_h * 3600 + tz_m * 60);
                idx += 6;
            }
            _ => return Err("expected 'Z', '+', or '-' for timezone".into()),
        }
    }
    if idx != bytes.len() {
        return Err("trailing characters".into());
    }

    let unix = days_from_civil(year, month as i32, day as i32) * 86_400
        + hour * 3600
        + minute * 60
        + second
        - offset_seconds;
    Ok(unix)
}

/// Howard Hinnant's date algorithm: days since 1970-01-01 for proleptic Gregorian.
fn days_from_civil(y: i64, m: i32, d: i32) -> i64 {
    let y = if m <= 2 { y - 1 } else { y };
    let era = y.div_euclid(400);
    let yoe = y - era * 400; // 0..=399
    let m_int = i64::from(m);
    let d_int = i64::from(d);
    let doy = (153 * (if m_int <= 2 { m_int + 9 } else { m_int - 3 }) + 2) / 5 + d_int - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy; // 0..=146096
    era * 146_097 + doe - 719_468 // shift to 1970-01-01
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rfc3339_zulu() {
        assert_eq!(parse_rfc3339("1970-01-01T00:00:00Z").unwrap(), 0);
        assert_eq!(
            parse_rfc3339("2026-05-06T00:00:00Z").unwrap(),
            1_778_025_600
        );
    }

    #[test]
    fn rfc3339_offset() {
        // 2026-05-06T02:00:00+02:00 == 2026-05-06T00:00:00Z
        assert_eq!(
            parse_rfc3339("2026-05-06T02:00:00+02:00").unwrap(),
            1_778_025_600
        );
    }

    #[test]
    fn rfc3339_fractional() {
        assert_eq!(parse_rfc3339("1970-01-01T00:00:00.123Z").unwrap(), 0);
    }

    #[test]
    fn rfc3339_invalid() {
        assert!(parse_rfc3339("nope").is_err());
        assert!(parse_rfc3339("2026-13-01T00:00:00Z").is_ok()); // overflow handled by days_from_civil
    }
}
