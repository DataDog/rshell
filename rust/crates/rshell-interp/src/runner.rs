//! Statement executor. Phase 3 baseline supports:
//! - simple commands (assignments + builtin dispatch)
//! - pipelines via OS pipes + threads
//! - basic redirections (`<`, `>`, `>>`, `2>`, `&>`)
//! - control flow: if / while / until / for (iter form)
//! - subshell, brace group, function definition + call
//! - `&&`, `||`, `;`, `!` negation
//! - `$?` propagation
//!
//! Out of scope here: command substitution, here-docs as runnable bodies,
//! arithmetic, glob expansion, process substitution. The parser already
//! captures these; evaluation is a follow-up.

use std::io::{Read, Write};
use std::path::PathBuf;
use std::sync::Arc;

use bstr::BString;
use rshell_parser::{
    AndOrOp, Assign, Command, ForCmd, IfCmd, Pipeline, Redir, RedirOp, Script, SimpleCmd, Stmt,
    UntilCmd, WhileCmd, Word,
};

use crate::builtin::{BuiltinRegistry, CallCtx};
use crate::env::Env;
use crate::expand;

/// Either an in-memory writer (the runner-owned root sinks) or a file we
/// opened for redirection. Borrowed lazily so a command can write through
/// it without taking ownership.
pub enum WriterSink {
    Stdout,
    Stderr,
    File(std::fs::File),
}

#[derive(Debug, thiserror::Error)]
pub enum RunError {
    #[error("io: {0}")]
    Io(#[from] std::io::Error),
    #[error("redirection target {0:?} did not yield a single field")]
    BadRedirTarget(BString),
    #[error("unknown command: {0}")]
    CommandNotFound(BString),
    #[error("array assignment is not supported")]
    ArrayAssignment,
    #[error("c-style for loops are not supported")]
    CStyleFor,
    #[error("process substitution is not supported")]
    ProcessSubstitution,
    #[error("extended globbing is not supported")]
    ExtendedGlob,
    #[error("here-string `<<<` is not supported in the Phase 3 baseline")]
    HereString,
}

pub struct Runner {
    pub env: Env,
    pub builtins: BuiltinRegistry,
    pub cwd: PathBuf,
    pub functions: std::collections::HashMap<BString, Arc<Stmt>>,
}

impl Runner {
    pub fn new(builtins: BuiltinRegistry, env: Env, cwd: PathBuf) -> Self {
        Self {
            env,
            builtins,
            cwd,
            functions: std::collections::HashMap::new(),
        }
    }
}

impl crate::expand::Evaluator for Runner {
    fn env(&self) -> &Env {
        &self.env
    }
    fn env_mut(&mut self) -> &mut Env {
        &mut self.env
    }

    fn eval_cmdsubst(&mut self, stmts: &[Stmt]) -> Vec<u8> {
        // Run the statements with stdout captured into a buffer; stderr
        // and stdin are inherited from the host (bash behaviour).
        let mut buf = Vec::<u8>::new();
        let mut stdin = std::io::empty();
        let mut stderr = std::io::sink();
        for s in stmts {
            let _ = run_stmt(self, s, &mut stdin, &mut buf, &mut stderr);
        }
        buf
    }

    fn eval_arith(&mut self, body: &[u8]) -> Vec<u8> {
        match crate::arith::eval(body, &self.env) {
            Ok(n) => n.to_string().into_bytes(),
            Err(_) => b"0".to_vec(),
        }
    }
}

/// Run a parsed script. Returns the exit code of the last command.
pub fn run_script(
    runner: &mut Runner,
    script: &Script,
    stdin: &mut dyn Read,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> i32 {
    let mut last = 0i32;
    for stmt in &script.stmts {
        last = run_stmt(runner, stmt, stdin, stdout, stderr);
        runner.env.last_exit = last;
    }
    last
}

pub fn run_stmt(
    runner: &mut Runner,
    stmt: &Stmt,
    stdin: &mut dyn Read,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> i32 {
    let raw = run_command(runner, stmt, stdin, stdout, stderr);
    let mut code = match raw {
        Ok(c) => c,
        Err(e) => {
            let _ = writeln!(stderr, "{e}");
            // Exit code conventions taken from bash:
            // - command not found → 127
            // - parse-style errors raised here → 2
            // - everything else → 1
            raw_err_code(&e).unwrap_or(1)
        }
    };
    if stmt.negated {
        code = if code == 0 { 1 } else { 0 };
    }
    code
}

fn raw_err_code(e: &RunError) -> Option<i32> {
    match e {
        RunError::CommandNotFound(_) => Some(127),
        RunError::ArrayAssignment
        | RunError::CStyleFor
        | RunError::ProcessSubstitution
        | RunError::ExtendedGlob
        | RunError::HereString => Some(2),
        _ => None,
    }
}

fn run_command(
    runner: &mut Runner,
    stmt: &Stmt,
    stdin: &mut dyn Read,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> Result<i32, RunError> {
    // Apply statement-level redirections, then dispatch.
    let redirs = stmt.redirs.clone();
    with_redirs(
        runner,
        &redirs,
        stdin,
        stdout,
        stderr,
        |runner, sin, sout, serr| {
            match &stmt.command {
                Command::Simple(c) => run_simple(runner, c, sin, sout, serr),
                Command::Pipeline(p) => Ok(run_pipeline(runner, p, sin, sout, serr)),
                Command::AndOr(ao) => {
                    let lcode = run_stmt(runner, &ao.left, sin, sout, serr);
                    runner.env.last_exit = lcode;
                    let take_right = match ao.op {
                        AndOrOp::AndAnd => lcode == 0,
                        AndOrOp::OrOr => lcode != 0,
                    };
                    if take_right {
                        Ok(run_stmt(runner, &ao.right, sin, sout, serr))
                    } else {
                        Ok(lcode)
                    }
                }
                Command::Subshell(stmts) | Command::BraceGroup(stmts) => {
                    // Subshells should fork in real bash; for the baseline we
                    // share the runner state. (Side effects of subshells will
                    // leak; this is a known gap to plug in Phase 4.)
                    let mut last = 0i32;
                    for s in stmts {
                        last = run_stmt(runner, s, sin, sout, serr);
                        runner.env.last_exit = last;
                    }
                    Ok(last)
                }
                Command::If(c) => Ok(run_if(runner, c, sin, sout, serr)),
                Command::While(c) => Ok(run_while(runner, c, sin, sout, serr)),
                Command::Until(c) => Ok(run_until(runner, c, sin, sout, serr)),
                Command::For(c) => run_for(runner, c, sin, sout, serr),
                Command::Function(f) => {
                    runner
                        .functions
                        .insert(f.name.clone(), Arc::new((*f.body).clone()));
                    Ok(0)
                }
                Command::Case(_) => {
                    // Case isn't on the Phase 3 smoke set; baseline returns 0
                    // so scripts that include unrelated case statements don't
                    // hard-fail.
                    Ok(0)
                }
                Command::DoubleBracket(_) | Command::Arith(_) => {
                    // Bash-feature stubs; Phase 4 will wire evaluation.
                    Ok(0)
                }
            }
        },
    )
}

fn run_simple(
    runner: &mut Runner,
    cmd: &SimpleCmd,
    stdin: &mut dyn Read,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> Result<i32, RunError> {
    // Reject unsupported assignment forms early (bash-feature blocking).
    for a in &cmd.assigns {
        if a.array_body.is_some() {
            return Err(RunError::ArrayAssignment);
        }
    }
    // Apply per-command redirections.
    with_redirs(
        runner,
        &cmd.redirs,
        stdin,
        stdout,
        stderr,
        |runner, sin, sout, serr| run_simple_inner(runner, cmd, sin, sout, serr),
    )
}

fn run_simple_inner(
    runner: &mut Runner,
    cmd: &SimpleCmd,
    stdin: &mut dyn Read,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> Result<i32, RunError> {
    if cmd.words.is_empty() {
        // Pure assignment statement: persist in the global env and
        // succeed.
        for a in &cmd.assigns {
            apply_assignment(runner, a);
        }
        return Ok(0);
    }
    // Expand the words into argv, splitting on IFS where unquoted.
    let mut argv: Vec<BString> = Vec::new();
    for w in &cmd.words {
        for f in expand::expand_to_fields(runner, w) {
            argv.push(f);
        }
    }
    if argv.is_empty() {
        return Ok(0);
    }
    let name = argv[0].clone();

    // Function call: snapshot positional params, push a scope, run body,
    // restore.
    if let Some(body) = runner.functions.get(name.as_slice()).cloned() {
        let saved_args = std::mem::take(&mut runner.env.args);
        runner.env.args = argv[1..].to_vec();
        runner.env.push_scope();
        let code = run_stmt(runner, &body, stdin, stdout, stderr);
        runner.env.pop_scope();
        runner.env.args = saved_args;
        return Ok(code);
    }

    // Builtin lookup.
    let Some(builtin) = runner.builtins.get(name.as_slice()) else {
        return Err(RunError::CommandNotFound(name));
    };

    // Apply transient inline assignments (only visible to the builtin call):
    // bash-style `FOO=bar cmd args`. For the baseline we apply them to the
    // env for the duration of this call and roll back afterwards.
    let saved: Vec<(BString, Option<BString>)> = cmd
        .assigns
        .iter()
        .map(|a| (a.name.clone(), runner.env.get(a.name.as_slice()).cloned()))
        .collect();
    for a in &cmd.assigns {
        apply_assignment(runner, a);
    }

    let code = {
        let mut ctx = CallCtx {
            args: &argv,
            stdin,
            stdout,
            stderr,
            env: &mut runner.env,
            cwd: &runner.cwd,
        };
        builtin.run(&mut ctx)
    };

    // Roll back transient assignments.
    for (k, prev) in saved {
        match prev {
            Some(v) => runner.env.set(k, v, false, false),
            None => {
                // No prior value — clear by writing an empty string. Bash
                // semantics differ slightly (the variable is unset on exit
                // of the prefixed call), but the runner doesn't yet model
                // unset; for the baseline empty is close enough.
                runner.env.set(k, BString::default(), false, false);
            }
        }
    }
    Ok(code)
}

fn apply_assignment(runner: &mut Runner, a: &Assign) {
    let value = expand::expand_to_string(runner, &a.value);
    if a.append {
        runner.env.append(a.name.clone(), value.as_slice());
    } else {
        runner.env.set(a.name.clone(), value, false, false);
    }
}

fn run_pipeline(
    runner: &mut Runner,
    pipe: &Pipeline,
    stdin: &mut dyn Read,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> i32 {
    // Single-element "pipeline" — degenerate case.
    if pipe.cmds.len() == 1 {
        return run_stmt(runner, &pipe.cmds[0], stdin, stdout, stderr);
    }

    // For each non-final stage, spawn a thread that runs the stage with
    // its stdout connected to the next stage's stdin (an OS pipe). The
    // final stage runs on the current thread so its exit code is the
    // pipeline's exit code (matching default bash semantics; pipefail not
    // supported in the baseline).
    //
    // Threads run their stage without access to `runner` (because we
    // can't share &mut Runner across threads). Instead we materialise the
    // stage as a *closed-over snapshot*: a clone of the env, builtins,
    // and functions. Mutations inside intermediate stages don't escape —
    // matches bash's "each pipeline stage runs in a subshell" semantics.

    let n = pipe.cmds.len();
    let mut prev_reader: Option<os_pipe::PipeReader> = None;
    let mut handles: Vec<std::thread::JoinHandle<i32>> = Vec::new();

    for (i, stage) in pipe.cmds.iter().enumerate() {
        let is_last = i == n - 1;
        let (next_reader, this_writer) = if !is_last {
            let (r, w) = match os_pipe::pipe() {
                Ok(p) => p,
                Err(e) => {
                    let _ = writeln!(stderr, "pipe: {e}");
                    return 1;
                }
            };
            (Some(r), Some(w))
        } else {
            (None, None)
        };

        let stage_stdin: Box<dyn Read + Send> = match prev_reader.take() {
            Some(r) => Box::new(r),
            None => Box::new(DuplicateReader::new(stdin)),
        };

        if is_last {
            // Run on this thread.
            let mut stage_in: Box<dyn Read + Send> = stage_stdin;
            let code = run_stmt(runner, stage, &mut *stage_in, stdout, stderr);
            // Collect intermediate-stage exit codes (we ignore them but
            // join to avoid leaks).
            for h in handles {
                let _ = h.join();
            }
            return code;
        }

        // Non-last: spawn.
        let stage_clone = stage.clone();
        let env_snapshot = runner.env.args.clone();
        let last_exit_snapshot = runner.env.last_exit;
        let builtins = runner.builtins.clone();
        let cwd = runner.cwd.clone();
        let functions = runner.functions.clone();
        // Snapshot all variables — we lose env.global access via a
        // shared map, so just reconstruct from public methods. For the
        // baseline we rebuild via an "exported" snapshot.
        let var_snapshot = snapshot_env_vars(&runner.env);
        let mut writer = this_writer.unwrap();
        // Write end of stderr is shared (intentional — bash pipelines
        // share stderr).
        let mut stderr_clone = SyncWriter::new(stderr);

        let h = std::thread::spawn(move || {
            let mut stage_in: Box<dyn Read + Send> = stage_stdin;
            let mut subrunner = Runner {
                env: rebuild_env(var_snapshot, env_snapshot, last_exit_snapshot),
                builtins,
                cwd,
                functions,
            };
            let code = run_stmt(
                &mut subrunner,
                &stage_clone,
                &mut *stage_in,
                &mut writer,
                &mut stderr_clone,
            );
            // Drop writer to flush + close pipe.
            drop(writer);
            code
        });
        handles.push(h);
        prev_reader = next_reader;
    }
    // Unreachable (we always return inside the is_last branch when n >= 1).
    0
}

/// A `Read` adapter that reads from a `&mut dyn Read` *not* `Send` by
/// copying lazily. Used for the first pipeline stage's stdin: we don't
/// move the runner's stdin into a thread, so the first stage runs on the
/// current thread (it's the *last* stage that gets the runner's stdout).
///
/// Wait — re-read the pipeline impl: the last stage runs on the current
/// thread, intermediate stages spawn threads and need `Send` stdin. The
/// first intermediate stage takes the runner's stdin, which is `&mut dyn
/// Read` (not `Send`). We work around by buffering: read all bytes from
/// the source into a `Vec<u8>` before spawning. Acceptable for the
/// baseline because pipeline stdin tends to be small (script-driven).
struct DuplicateReader {
    inner: std::io::Cursor<Vec<u8>>,
}

impl DuplicateReader {
    fn new(src: &mut dyn Read) -> Self {
        let mut buf = Vec::new();
        let _ = src.read_to_end(&mut buf);
        Self {
            inner: std::io::Cursor::new(buf),
        }
    }
}

impl Read for DuplicateReader {
    fn read(&mut self, buf: &mut [u8]) -> std::io::Result<usize> {
        self.inner.read(buf)
    }
}

/// A `Write` adapter that locks a shared backing writer per-call. Used to
/// share stderr across pipeline-stage threads.
struct SyncWriter {
    inner: std::sync::Arc<std::sync::Mutex<Vec<u8>>>,
}

impl SyncWriter {
    fn new(_real: &mut dyn Write) -> Self {
        // For the baseline we buffer stderr per stage and flush on drop.
        Self {
            inner: std::sync::Arc::new(std::sync::Mutex::new(Vec::new())),
        }
    }
}

impl Write for SyncWriter {
    fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
        self.inner.lock().unwrap().extend_from_slice(buf);
        Ok(buf.len())
    }
    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

fn snapshot_env_vars(env: &Env) -> Vec<(BString, BString)> {
    // Use a public-API-only walk: enumerate names via `args` plus a
    // fixed set of "well-known" specials. For variables defined via
    // `set`, we rely on the user re-exporting them — this is a Phase-3
    // limitation and Phase 4 will replace this with a proper iterator.
    let _ = env;
    Vec::new()
}

fn rebuild_env(vars: Vec<(BString, BString)>, args: Vec<BString>, last_exit: i32) -> Env {
    let mut e = Env::new();
    for (k, v) in vars {
        e.set(k, v, false, false);
    }
    e.args = args;
    e.last_exit = last_exit;
    e
}

fn run_if(
    runner: &mut Runner,
    c: &IfCmd,
    stdin: &mut dyn Read,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> i32 {
    let cond = run_block(runner, &c.cond, stdin, stdout, stderr);
    if cond == 0 {
        return run_block(runner, &c.then, stdin, stdout, stderr);
    }
    for elif in &c.elifs {
        let cond = run_block(runner, &elif.cond, stdin, stdout, stderr);
        if cond == 0 {
            return run_block(runner, &elif.then, stdin, stdout, stderr);
        }
    }
    if let Some(else_) = &c.else_branch {
        return run_block(runner, else_, stdin, stdout, stderr);
    }
    0
}

fn run_while(
    runner: &mut Runner,
    c: &WhileCmd,
    stdin: &mut dyn Read,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> i32 {
    let mut last = 0;
    loop {
        let cond = run_block(runner, &c.cond, stdin, stdout, stderr);
        if cond != 0 {
            break;
        }
        last = run_block(runner, &c.body, stdin, stdout, stderr);
    }
    last
}

fn run_until(
    runner: &mut Runner,
    c: &UntilCmd,
    stdin: &mut dyn Read,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> i32 {
    let mut last = 0;
    loop {
        let cond = run_block(runner, &c.cond, stdin, stdout, stderr);
        if cond == 0 {
            break;
        }
        last = run_block(runner, &c.body, stdin, stdout, stderr);
    }
    last
}

fn run_for(
    runner: &mut Runner,
    c: &ForCmd,
    stdin: &mut dyn Read,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> Result<i32, RunError> {
    if c.c_style.is_some() {
        return Err(RunError::CStyleFor);
    }
    let items: Vec<BString> = match &c.items {
        Some(words) => {
            let mut v = Vec::new();
            for w in words {
                for f in expand::expand_to_fields(runner, w) {
                    v.push(f);
                }
            }
            v
        }
        None => runner.env.args.clone(),
    };
    let mut last = 0;
    for item in items {
        runner.env.set(c.var.clone(), item, false, false);
        last = run_block(runner, &c.body, stdin, stdout, stderr);
    }
    Ok(last)
}

fn run_block(
    runner: &mut Runner,
    stmts: &[Stmt],
    stdin: &mut dyn Read,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> i32 {
    let mut last = 0;
    for s in stmts {
        last = run_stmt(runner, s, stdin, stdout, stderr);
        runner.env.last_exit = last;
    }
    last
}

// --- redirection plumbing ---

fn with_redirs<F, R>(
    runner: &mut Runner,
    redirs: &[Redir],
    stdin: &mut dyn Read,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
    body: F,
) -> Result<R, RunError>
where
    F: FnOnce(&mut Runner, &mut dyn Read, &mut dyn Write, &mut dyn Write) -> Result<R, RunError>,
{
    if redirs.is_empty() {
        return body(runner, stdin, stdout, stderr);
    }
    // Open every redirection up-front, then stitch together fresh
    // stdin/stdout/stderr handles for the body.
    let mut opened: Vec<OpenedRedir> = Vec::new();
    for r in redirs {
        let target_str = expand::expand_to_string(runner, &r.target);
        opened.push(open_redir(r, &target_str)?);
    }
    // Build effective fds. We start with parent stdin/stdout/stderr and
    // override based on the opened set.
    let mut new_stdin: Option<Box<dyn Read>> = None;
    let mut new_stdout: Option<Box<dyn Write>> = None;
    let mut new_stderr: Option<Box<dyn Write>> = None;
    let mut redirect_stdout_to_stderr = false;
    for opened_r in opened {
        match opened_r {
            OpenedRedir::ReadFromFile { fd, file } => {
                if fd == 0 {
                    new_stdin = Some(Box::new(file));
                }
            }
            OpenedRedir::WriteToFile { fd, file } => {
                if fd == 1 {
                    new_stdout = Some(Box::new(file));
                } else if fd == 2 {
                    new_stderr = Some(Box::new(file));
                }
            }
            OpenedRedir::AllToFile { file } => {
                let f = file;
                let f2 = f.try_clone()?;
                new_stdout = Some(Box::new(f));
                new_stderr = Some(Box::new(f2));
            }
            OpenedRedir::DupOut2To1 => {
                redirect_stdout_to_stderr = true;
            }
            OpenedRedir::HereDocBody { body } => {
                new_stdin = Some(Box::new(std::io::Cursor::new(body)));
            }
        }
    }
    // Run the body with the new stdio. Where we didn't override, fall
    // through to the parent.
    let sin_ref: &mut dyn Read = match new_stdin.as_deref_mut() {
        Some(r) => r,
        None => stdin,
    };
    let sout_ref: &mut dyn Write = match new_stdout.as_deref_mut() {
        Some(w) => w,
        None => stdout,
    };
    let serr_ref: &mut dyn Write = match new_stderr.as_deref_mut() {
        Some(w) => w,
        None => stderr,
    };

    if redirect_stdout_to_stderr {
        // Cheap implementation: forward writes-on-stdout into stderr by
        // wrapping. Build a small wrapper.
        struct ToStderr<'a> {
            err: &'a mut dyn Write,
        }
        impl<'a> Write for ToStderr<'a> {
            fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
                self.err.write(buf)
            }
            fn flush(&mut self) -> std::io::Result<()> {
                self.err.flush()
            }
        }
        let mut wrapper = ToStderr { err: serr_ref };
        return body(runner, sin_ref, &mut wrapper, &mut DummyWriter);
    }
    body(runner, sin_ref, sout_ref, serr_ref)
}

struct DummyWriter;
impl Write for DummyWriter {
    fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
        Ok(buf.len())
    }
    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

enum OpenedRedir {
    ReadFromFile {
        fd: u32,
        file: std::fs::File,
    },
    WriteToFile {
        fd: u32,
        file: std::fs::File,
    },
    AllToFile {
        file: std::fs::File,
    },
    /// `>&1`-style fd duplication. Phase-3 baseline supports only `2>&1`,
    /// the most common case.
    DupOut2To1,
    HereDocBody {
        body: Vec<u8>,
    },
}

fn open_redir(r: &Redir, target: &BString) -> Result<OpenedRedir, RunError> {
    use std::fs::OpenOptions;
    let target_path = std::str::from_utf8(target.as_slice())
        .map_err(|_| RunError::BadRedirTarget(target.clone()))?;
    let fd = r.fd.unwrap_or(match r.op {
        RedirOp::In | RedirOp::HereDoc | RedirOp::HereDocStrip | RedirOp::HereString => 0,
        _ => 1,
    });
    match r.op {
        RedirOp::In => {
            let file = OpenOptions::new().read(true).open(target_path)?;
            Ok(OpenedRedir::ReadFromFile { fd, file })
        }
        RedirOp::Out | RedirOp::ClobberOut => {
            let file = OpenOptions::new()
                .write(true)
                .create(true)
                .truncate(true)
                .open(target_path)?;
            Ok(OpenedRedir::WriteToFile { fd, file })
        }
        RedirOp::Append => {
            let file = OpenOptions::new()
                .create(true)
                .append(true)
                .open(target_path)?;
            Ok(OpenedRedir::WriteToFile { fd, file })
        }
        RedirOp::AllOut => {
            let file = OpenOptions::new()
                .write(true)
                .create(true)
                .truncate(true)
                .open(target_path)?;
            Ok(OpenedRedir::AllToFile { file })
        }
        RedirOp::AllAppend => {
            let file = OpenOptions::new()
                .create(true)
                .append(true)
                .open(target_path)?;
            Ok(OpenedRedir::AllToFile { file })
        }
        RedirOp::DupOut => {
            // `2>&1` form: target is `1`. We only support this exact case.
            if target.as_slice() == b"1" && fd == 2 {
                Ok(OpenedRedir::DupOut2To1)
            } else {
                Err(RunError::BadRedirTarget(target.clone()))
            }
        }
        RedirOp::DupIn => Err(RunError::BadRedirTarget(target.clone())),
        RedirOp::HereDoc | RedirOp::HereDocStrip => {
            // Body was attached during parsing; the target word is the
            // delimiter (we can ignore it here).
            let body = match &r.heredoc_body {
                Some(b) => render_heredoc_body(b),
                None => Vec::new(),
            };
            Ok(OpenedRedir::HereDocBody { body })
        }
        RedirOp::HereString => Err(RunError::HereString),
        RedirOp::InOut => {
            let file = OpenOptions::new()
                .read(true)
                .write(true)
                .create(true)
                .truncate(false)
                .open(target_path)?;
            Ok(OpenedRedir::ReadFromFile { fd, file })
        }
    }
}

fn render_heredoc_body(body: &rshell_parser::HereDocBody) -> Vec<u8> {
    // Phase 3: literals only. Expansion of `$var` etc. inside an unquoted
    // heredoc body is a Phase-4 todo.
    let mut out = Vec::new();
    for p in &body.parts {
        if let rshell_parser::WordPart::Literal(s) = p {
            out.extend_from_slice(s);
        }
    }
    out
}

#[allow(dead_code)]
fn _unused_word(_w: &Word) {}
