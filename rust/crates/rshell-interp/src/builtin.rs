//! Builtin command interface and registry.

use std::collections::HashMap;
use std::io::{Read, Write};
use std::sync::Arc;

use bstr::BString;

use crate::env::Env;

/// Per-call context handed to a builtin.
pub struct CallCtx<'a> {
    pub args: &'a [BString],
    pub stdin: &'a mut dyn Read,
    pub stdout: &'a mut dyn Write,
    pub stderr: &'a mut dyn Write,
    pub env: &'a mut Env,
    /// Current working directory (absolute, normalised).
    pub cwd: &'a std::path::Path,
}

pub trait Builtin: Send + Sync {
    /// Return value: the builtin's exit code (0–255). I/O errors should
    /// be written to `stderr` and returned as 1, mirroring the Go
    /// implementation.
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32;
}

#[derive(Default, Clone)]
pub struct BuiltinRegistry {
    map: HashMap<BString, Arc<dyn Builtin>>,
}

impl BuiltinRegistry {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn register<B: Builtin + 'static>(&mut self, name: impl Into<BString>, b: B) {
        self.map.insert(name.into(), Arc::new(b));
    }

    pub fn get(&self, name: &[u8]) -> Option<Arc<dyn Builtin>> {
        self.map.get(name).cloned()
    }

    pub fn names(&self) -> impl Iterator<Item = &BString> {
        self.map.keys()
    }
}
