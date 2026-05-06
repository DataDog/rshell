//! YAML scenario schema. Mirrors `tests/scenarios_test.go`.

use indexmap::IndexMap;
use serde::{Deserialize, Deserializer};

#[derive(Debug, Default, Deserialize)]
pub struct Scenario {
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub skip_assert_against_bash: bool,
    #[serde(default)]
    pub containerized: bool,
    #[serde(default)]
    pub setup: Setup,
    #[serde(default)]
    pub input: Input,
    #[serde(default)]
    pub expect: Expected,
}

#[derive(Debug, Default, Deserialize)]
pub struct Setup {
    #[serde(default)]
    pub files: Vec<SetupFile>,
}

#[derive(Debug, Default, Deserialize)]
pub struct SetupFile {
    pub path: String,
    #[serde(default)]
    pub content: String,
    /// File mode in YAML — written as e.g. `0644`. YAML 1.2 parsers (including
    /// `serde_yaml_ng`) treat a leading-zero number as a string, so we accept
    /// both ints and strings here. Strings with a leading `0` are interpreted
    /// as octal to match POSIX `chmod` conventions.
    #[serde(default, deserialize_with = "deserialize_chmod")]
    pub chmod: Option<u32>,
    /// If set, create a symbolic link with this target instead of a regular file.
    #[serde(default)]
    pub symlink: Option<String>,
    /// Optional RFC3339 timestamp; when set, override the file's mtime/atime.
    #[serde(default)]
    pub mod_time: Option<String>,
}

#[derive(Debug, Default, Deserialize)]
pub struct Input {
    /// OS-level environment variables. Applied to the subprocess only when
    /// running against bash for comparison; ignored for the rshell subprocess
    /// (the restricted interpreter starts with an empty environment).
    #[serde(default)]
    pub envs: IndexMap<String, String>,
    /// Initial environment of the restricted interpreter. Not yet supported in
    /// subprocess mode (the Go binary does not expose `--interpreter-env`).
    #[serde(default)]
    pub interpreter_env: IndexMap<String, String>,
    #[serde(default)]
    pub script: String,
    /// Allowed paths. Each entry is either `$DIR` (the test temp directory),
    /// an absolute path (e.g. `/proc/net`), or a path relative to the temp dir.
    #[serde(default)]
    pub allowed_paths: Option<Vec<String>>,
    /// Allowed command names. When empty and `allow_all_commands` is unset,
    /// the runner defaults to allow-all (matching the Go test harness).
    #[serde(default)]
    pub allowed_commands: Vec<String>,
    #[serde(default)]
    pub allow_all_commands: Option<bool>,
}

#[derive(Debug, Default, Deserialize)]
pub struct Expected {
    #[serde(default)]
    pub stdout: String,
    #[serde(default)]
    pub stdout_unordered: String,
    #[serde(default)]
    pub stdout_windows: Option<String>,
    #[serde(default)]
    pub stdout_contains: Vec<String>,
    #[serde(default)]
    pub stdout_contains_windows: Vec<String>,
    #[serde(default)]
    pub stderr: String,
    #[serde(default)]
    pub stderr_windows: Option<String>,
    #[serde(default)]
    pub stderr_contains: Vec<String>,
    #[serde(default)]
    pub stderr_contains_windows: Vec<String>,
    #[serde(default)]
    pub exit_code: i32,
}

impl Scenario {
    pub fn from_yaml(text: &str) -> Result<Self, serde_yaml_ng::Error> {
        serde_yaml_ng::from_str(text)
    }
}

fn deserialize_chmod<'de, D: Deserializer<'de>>(d: D) -> Result<Option<u32>, D::Error> {
    use serde::de::Error;

    #[derive(Deserialize)]
    #[serde(untagged)]
    enum Raw {
        Int(i64),
        Str(String),
    }

    let raw = Option::<Raw>::deserialize(d)?;
    match raw {
        None => Ok(None),
        Some(Raw::Int(n)) => u32::try_from(n)
            .map(Some)
            .map_err(|_| D::Error::custom(format!("chmod out of u32 range: {n}"))),
        Some(Raw::Str(s)) => {
            let trimmed = s.trim();
            let (radix, body) = if let Some(rest) = trimmed.strip_prefix("0o") {
                (8, rest)
            } else if let Some(rest) = trimmed.strip_prefix("0x") {
                (16, rest)
            } else if trimmed.starts_with('0') && trimmed.len() > 1 {
                (8, trimmed)
            } else {
                (10, trimmed)
            };
            u32::from_str_radix(body, radix)
                .map(Some)
                .map_err(|_| D::Error::custom(format!("invalid chmod string: {s:?}")))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_minimal_scenario() {
        let yaml = r#"
description: minimal
input:
  script: |+
    echo hello
expect:
  stdout: |+
    hello
  exit_code: 0
"#;
        let sc = Scenario::from_yaml(yaml).expect("parse");
        assert_eq!(sc.description, "minimal");
        assert_eq!(sc.input.script, "echo hello\n");
        assert_eq!(sc.expect.stdout, "hello\n");
        assert_eq!(sc.expect.exit_code, 0);
    }

    #[test]
    fn parses_setup_files_and_allowed_paths() {
        let yaml = r#"
setup:
  files:
    - path: a.txt
      content: hi
      chmod: 0644
    - path: link
      symlink: a.txt
input:
  script: cat a.txt
  allowed_paths: ["$DIR"]
  allowed_commands: ["rshell:cat"]
expect:
  stdout: hi
"#;
        let sc = Scenario::from_yaml(yaml).expect("parse");
        assert_eq!(sc.setup.files.len(), 2);
        assert_eq!(sc.setup.files[0].path, "a.txt");
        assert_eq!(sc.setup.files[0].chmod, Some(0o644));
        assert_eq!(sc.setup.files[1].symlink.as_deref(), Some("a.txt"));
        assert_eq!(
            sc.input.allowed_paths.as_deref(),
            Some(&["$DIR".into()][..])
        );
    }
}
