//! Assertion engine. Mirrors `assertExpectations` in `tests/scenarios_test.go`.

use crate::scenario::Scenario;

#[derive(Debug, thiserror::Error)]
pub enum AssertError {
    #[error("exit code mismatch: want {want}, got {got}")]
    ExitCode { want: i32, got: i32 },
    #[error("stdout mismatch:\n--- want ---\n{want}\n--- got ---\n{got}")]
    Stdout { want: String, got: String },
    #[error("stdout missing substring {needle:?}\n--- got ---\n{got}")]
    StdoutContains { needle: String, got: String },
    #[error("stdout (unordered) mismatch:\n--- want ---\n{want:?}\n--- got ---\n{got:?}")]
    StdoutUnordered { want: Vec<String>, got: Vec<String> },
    #[error("stderr mismatch:\n--- want ---\n{want}\n--- got ---\n{got}")]
    Stderr { want: String, got: String },
    #[error("stderr missing substring {needle:?}\n--- got ---\n{got}")]
    StderrContains { needle: String, got: String },
}

/// Apply the scenario expectations against captured outputs.
///
/// `is_windows` selects the `*_windows` overrides; pass `cfg!(windows)` from
/// the caller.
pub fn assert_expectations(
    scenario: &Scenario,
    stdout: &str,
    stderr: &str,
    exit_code: i32,
    is_windows: bool,
) -> Result<(), AssertError> {
    if scenario.expect.exit_code != exit_code {
        return Err(AssertError::ExitCode {
            want: scenario.expect.exit_code,
            got: exit_code,
        });
    }

    let stdout_contains: &[String] =
        if is_windows && !scenario.expect.stdout_contains_windows.is_empty() {
            &scenario.expect.stdout_contains_windows
        } else {
            &scenario.expect.stdout_contains
        };
    let stderr_contains: &[String] =
        if is_windows && !scenario.expect.stderr_contains_windows.is_empty() {
            &scenario.expect.stderr_contains_windows
        } else {
            &scenario.expect.stderr_contains
        };

    let expected_stdout = if is_windows {
        scenario
            .expect
            .stdout_windows
            .as_deref()
            .unwrap_or(scenario.expect.stdout.as_str())
    } else {
        scenario.expect.stdout.as_str()
    };
    let expected_stderr = if is_windows {
        scenario
            .expect
            .stderr_windows
            .as_deref()
            .unwrap_or(scenario.expect.stderr.as_str())
    } else {
        scenario.expect.stderr.as_str()
    };

    if !stdout_contains.is_empty() {
        for needle in stdout_contains {
            if !stdout.contains(needle) {
                return Err(AssertError::StdoutContains {
                    needle: needle.clone(),
                    got: stdout.to_owned(),
                });
            }
        }
    } else if !scenario.expect.stdout_unordered.is_empty() {
        let mut want: Vec<String> = scenario
            .expect
            .stdout_unordered
            .split('\n')
            .map(str::to_owned)
            .collect();
        let mut got: Vec<String> = stdout.split('\n').map(str::to_owned).collect();
        want.sort();
        got.sort();
        if want != got {
            return Err(AssertError::StdoutUnordered { want, got });
        }
    } else if expected_stdout != stdout {
        return Err(AssertError::Stdout {
            want: expected_stdout.to_owned(),
            got: stdout.to_owned(),
        });
    }

    if !stderr_contains.is_empty() {
        for needle in stderr_contains {
            if !stderr.contains(needle) {
                return Err(AssertError::StderrContains {
                    needle: needle.clone(),
                    got: stderr.to_owned(),
                });
            }
        }
    } else if expected_stderr != stderr {
        return Err(AssertError::Stderr {
            want: expected_stderr.to_owned(),
            got: stderr.to_owned(),
        });
    }

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::scenario::Scenario;

    fn sc(yaml: &str) -> Scenario {
        Scenario::from_yaml(yaml).expect("parse")
    }

    #[test]
    fn exact_stdout_passes() {
        let s = sc("expect:\n  stdout: hi\n  exit_code: 0\n");
        assert!(assert_expectations(&s, "hi", "", 0, false).is_ok());
    }

    #[test]
    fn exit_code_mismatch_fails() {
        let s = sc("expect:\n  exit_code: 1\n");
        let err = assert_expectations(&s, "", "", 0, false).unwrap_err();
        assert!(matches!(err, AssertError::ExitCode { want: 1, got: 0 }));
    }

    #[test]
    fn stdout_contains_takes_priority_over_stdout() {
        let s = sc("expect:\n  stdout: ignored\n  stdout_contains:\n    - hello\n    - world\n");
        assert!(assert_expectations(&s, "hello world", "", 0, false).is_ok());
    }

    #[test]
    fn unordered_stdout_matches() {
        let s = sc("expect:\n  stdout_unordered: \"a\\nb\\nc\"\n");
        assert!(assert_expectations(&s, "c\nb\na", "", 0, false).is_ok());
    }

    #[test]
    fn windows_override_used_on_windows() {
        let s = sc("expect:\n  stdout: unix\n  stdout_windows: win\n");
        assert!(assert_expectations(&s, "win", "", 0, true).is_ok());
        assert!(assert_expectations(&s, "unix", "", 0, false).is_ok());
    }
}
