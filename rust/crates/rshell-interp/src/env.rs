//! Variable environment for the shell. Supports global scope, function
//! scope (a stack of nested frames), exported flag, and read-only flag.

use std::collections::HashMap;

use bstr::BString;

#[derive(Debug, Clone, Default)]
pub struct Variable {
    pub value: BString,
    pub exported: bool,
    pub readonly: bool,
}

/// A scope frame. The top of the stack is the most recent scope (e.g. a
/// function body); lookups walk the stack from top to bottom and fall
/// back to the global frame.
#[derive(Debug, Default)]
pub struct Frame {
    pub vars: HashMap<BString, Variable>,
}

#[derive(Debug, Default)]
pub struct Env {
    /// Global variables.
    global: Frame,
    /// Function-local frames (innermost last).
    locals: Vec<Frame>,
    /// Positional parameters: `$1`..`$9`, `$0`.
    pub args: Vec<BString>,
    /// Last command exit code (`$?`).
    pub last_exit: i32,
    /// Process ID (`$$`).
    pub pid: u32,
}

impl Env {
    pub fn new() -> Self {
        Self {
            pid: std::process::id(),
            ..Self::default()
        }
    }

    pub fn from_pairs<I, K, V>(pairs: I) -> Self
    where
        I: IntoIterator<Item = (K, V)>,
        K: Into<BString>,
        V: Into<BString>,
    {
        let mut e = Self::new();
        for (k, v) in pairs {
            e.set(k.into(), v.into(), true, false);
        }
        e
    }

    pub fn push_scope(&mut self) {
        self.locals.push(Frame::default());
    }

    pub fn pop_scope(&mut self) {
        self.locals.pop();
    }

    /// Look up a variable, walking from the innermost local frame to the
    /// global frame.
    pub fn get(&self, name: &[u8]) -> Option<&BString> {
        for f in self.locals.iter().rev() {
            if let Some(v) = f.vars.get(name) {
                return Some(&v.value);
            }
        }
        self.global.vars.get(name).map(|v| &v.value)
    }

    /// Set a variable. If `name` exists in any local scope, update there;
    /// otherwise update or create in the global scope.
    pub fn set(&mut self, name: BString, value: BString, exported: bool, readonly: bool) {
        for f in self.locals.iter_mut().rev() {
            if let Some(v) = f.vars.get_mut(name.as_slice()) {
                if v.readonly {
                    return;
                }
                v.value = value;
                if exported {
                    v.exported = true;
                }
                if readonly {
                    v.readonly = true;
                }
                return;
            }
        }
        let entry = self.global.vars.entry(name).or_default();
        if entry.readonly {
            return;
        }
        entry.value = value;
        if exported {
            entry.exported = true;
        }
        if readonly {
            entry.readonly = true;
        }
    }

    /// Append to an existing variable (used for `name+=value`). Creates
    /// the variable if it does not exist.
    pub fn append(&mut self, name: BString, suffix: &[u8]) {
        let cur = self.get(name.as_slice()).cloned().unwrap_or_default();
        let mut new = cur;
        new.extend_from_slice(suffix);
        self.set(name, new, false, false);
    }

    /// Declare a variable in the innermost local scope (creates if missing).
    pub fn declare_local(&mut self, name: BString, value: BString) {
        if let Some(top) = self.locals.last_mut() {
            top.vars.insert(
                name,
                Variable {
                    value,
                    ..Default::default()
                },
            );
        } else {
            self.set(name, value, false, false);
        }
    }

    /// Number of positional parameters (`$#`).
    pub fn argc(&self) -> usize {
        self.args.len()
    }
}
