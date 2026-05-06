//! Subprocess driver: runs a scenario against an external `rshell`-compatible
//! binary, captures stdout/stderr/exit code, and returns the outcome.

use std::io::Write;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::Duration;

use crate::assert::{AssertError, assert_expectations};
use crate::scenario::Scenario;
use crate::setup;

/// Reasons a scenario can't be exercised in subprocess mode.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SkipReason {
    InterpreterEnvUnsupported,
    ContainerizedUnsupported,
    SymlinkOnWindows,
    AllowedPathsCwdMismatch,
}

impl SkipReason {
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::InterpreterEnvUnsupported => {
                "scenario sets `interpreter_env` (no --interpreter-env flag on the binary)"
            }
            Self::ContainerizedUnsupported => {
                "scenario sets `containerized: true` (no --host-prefix flag on the binary)"
            }
            Self::SymlinkOnWindows => "scenario uses symlinks (skipped on Windows)",
            Self::AllowedPathsCwdMismatch => {
                "scenario's first `allowed_paths` entry isn't `$DIR`; the binary uses it as CWD \
                 while the in-process Go test forces CWD=$DIR — divergence is structural and the \
                 subprocess CLI cannot match it without a `--workdir` flag"
            }
        }
    }
}

#[derive(Debug, thiserror::Error)]
pub enum RunError {
    #[error("setup: {0}")]
    Setup(#[from] crate::setup::SetupError),
    #[error("subprocess spawn: {0}")]
    Spawn(#[from] std::io::Error),
    #[error("subprocess timed out after {0:?}")]
    Timeout(Duration),
    #[error("assertion: {0}")]
    Assert(#[from] AssertError),
}

#[derive(Debug)]
pub enum Outcome {
    Passed,
    Skipped(SkipReason),
    Failed(RunError),
}

#[derive(Debug, Clone)]
pub struct RunOptions {
    pub binary: PathBuf,
    /// Per-scenario timeout. The Go interpreter has its own 30s cap; this is
    /// a belt-and-suspenders limit on the subprocess itself.
    pub timeout: Duration,
}

impl Default for RunOptions {
    fn default() -> Self {
        Self {
            binary: PathBuf::from("rshell"),
            timeout: Duration::from_secs(60),
        }
    }
}

pub fn run_scenario(scenario: &Scenario, opts: &RunOptions) -> Outcome {
    if !scenario.input.interpreter_env.is_empty() {
        return Outcome::Skipped(SkipReason::InterpreterEnvUnsupported);
    }
    if scenario.containerized {
        return Outcome::Skipped(SkipReason::ContainerizedUnsupported);
    }
    if cfg!(windows) && scenario.setup.files.iter().any(|f| f.symlink.is_some()) {
        return Outcome::Skipped(SkipReason::SymlinkOnWindows);
    }
    if let Some(paths) = scenario.input.allowed_paths.as_deref() {
        // The in-process Go test sets runner.Dir = $DIR explicitly and then
        // applies AllowedPaths. The binary, lacking a --workdir flag, picks
        // the first allowed path as CWD. Whenever the first entry is anything
        // other than $DIR, the two CWDs differ and CWD-relative scripts diverge.
        if !paths.is_empty() && paths[0] != "$DIR" {
            return Outcome::Skipped(SkipReason::AllowedPathsCwdMismatch);
        }
    }

    let temp = match tempfile::tempdir() {
        Ok(t) => t,
        Err(e) => return Outcome::Failed(RunError::Spawn(e)),
    };
    let dir = temp.path();

    if let Err(e) = setup::apply(dir, scenario) {
        return Outcome::Failed(RunError::Setup(e));
    }

    let resolved_paths = resolve_allowed_paths(dir, scenario.input.allowed_paths.as_deref());

    let (stdout, stderr, exit_code) = match invoke_binary(
        &opts.binary,
        dir,
        scenario,
        resolved_paths.as_deref(),
        opts.timeout,
    ) {
        Ok(t) => t,
        Err(e) => return Outcome::Failed(e),
    };

    match assert_expectations(scenario, &stdout, &stderr, exit_code, cfg!(windows)) {
        Ok(()) => Outcome::Passed,
        Err(e) => Outcome::Failed(RunError::Assert(e)),
    }
}

fn resolve_allowed_paths(dir: &Path, raw: Option<&[String]>) -> Option<Vec<String>> {
    let raw = raw?;
    let mut out = Vec::with_capacity(raw.len());
    for p in raw {
        if p == "$DIR" {
            out.push(dir.to_string_lossy().into_owned());
        } else if Path::new(p).is_absolute() || p.starts_with('/') {
            // Absolute paths are used as-is, but only if they exist on this OS.
            // (`/proc/net` won't exist on macOS or Windows.)
            if Path::new(p).exists() {
                out.push(p.clone());
            }
        } else {
            out.push(dir.join(p).to_string_lossy().into_owned());
        }
    }
    Some(out)
}

fn invoke_binary(
    binary: &Path,
    cwd: &Path,
    scenario: &Scenario,
    allowed_paths: Option<&[String]>,
    timeout: Duration,
) -> Result<(String, String, i32), RunError> {
    let mut cmd = Command::new(binary);
    cmd.current_dir(cwd);
    // Restricted-shell semantics: pass an empty environment to the subprocess
    // unless the scenario explicitly sets OS-level vars via `envs:`. Mirrors
    // the in-process Go test, which does not propagate the host environment.
    cmd.env_clear();
    for (k, v) in &scenario.input.envs {
        cmd.env(k, v);
    }

    if let Some(paths) = allowed_paths {
        cmd.arg("-p").arg(paths.join(","));
    }

    if !scenario.input.allowed_commands.is_empty() {
        cmd.arg("--allowed-commands")
            .arg(scenario.input.allowed_commands.join(","));
    } else if scenario.input.allow_all_commands.unwrap_or(true) {
        // Default to allow-all to match the Go test harness's backward-compat
        // behaviour. When `allow_all_commands: false` is explicit and no
        // allowed_commands list is provided, omit both flags so the binary
        // blocks everything (matches in-process behaviour).
        cmd.arg("--allow-all-commands");
    }

    cmd.stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());

    let mut child = cmd.spawn()?;

    // Spawn reader threads so the subprocess never blocks on a full pipe buffer.
    let stdout_pipe = child.stdout.take();
    let stderr_pipe = child.stderr.take();
    let stdout_handle = std::thread::spawn(move || -> std::io::Result<Vec<u8>> {
        let mut buf = Vec::new();
        if let Some(mut s) = stdout_pipe {
            use std::io::Read;
            s.read_to_end(&mut buf)?;
        }
        Ok(buf)
    });
    let stderr_handle = std::thread::spawn(move || -> std::io::Result<Vec<u8>> {
        let mut buf = Vec::new();
        if let Some(mut s) = stderr_pipe {
            use std::io::Read;
            s.read_to_end(&mut buf)?;
        }
        Ok(buf)
    });

    if let Some(mut stdin) = child.stdin.take() {
        // Best-effort write. If the subprocess exits before consuming all of stdin
        // (e.g. parse error fails fast), the broken-pipe error is non-fatal.
        let _ = stdin.write_all(scenario.input.script.as_bytes());
        // Drop closes the pipe so the binary sees EOF.
    }

    let start = std::time::Instant::now();
    let status = loop {
        match child.try_wait()? {
            Some(s) => break s,
            None => {
                if start.elapsed() >= timeout {
                    let _ = child.kill();
                    let _ = child.wait();
                    return Err(RunError::Timeout(timeout));
                }
                std::thread::sleep(Duration::from_millis(20));
            }
        }
    };

    let stdout = stdout_handle
        .join()
        .map_err(|_| std::io::Error::other("stdout reader panicked"))??;
    let stderr = stderr_handle
        .join()
        .map_err(|_| std::io::Error::other("stderr reader panicked"))??;

    let code = status.code().unwrap_or({
        #[cfg(unix)]
        {
            use std::os::unix::process::ExitStatusExt;
            128 + status.signal().unwrap_or(0)
        }
        #[cfg(not(unix))]
        {
            -1
        }
    });
    Ok((
        String::from_utf8_lossy(&stdout).into_owned(),
        String::from_utf8_lossy(&stderr).into_owned(),
        code,
    ))
}
