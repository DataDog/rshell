// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

// DEMO ONLY: this file enables rshell to execute a small allowlist of host
// binaries. Running host binaries fundamentally changes rshell's threat
// model — the entire reason rshell exists is to *not* execute host binaries.
// This entry-point exists for the host-remediation demo (see
// docs/RULES.md / SHELL_FEATURES.md "demo only" section) and would need a
// real design pass before becoming a product feature.

package interp

import (
	"context"
	"errors"
	"os/exec"
)

// hostEnvAllowlist is the set of environment variable names forwarded to
// host binaries. Anything else in the runner environment is stripped so
// that host invocations do not leak ambient configuration that the rest
// of the shell deliberately keeps out.
var hostEnvAllowlist = []string{"PATH", "HOME", "LANG"}

// runHostCommand executes the host binary at path with args[1:] as its argv,
// plumbing the runner's stdin/stdout/stderr through. It returns the binary's
// exit code as a uint8 (so $? works) and propagates context cancellation by
// killing the process with SIGKILL — exec.CommandContext's default Cancel
// uses os.Kill (SIGKILL on Unix), which matches the timeout behaviour the
// rest of the runner applies to builtins.
//
// args[0] is the user-visible command name; args[1:] is the binary's argv.
// The binary path itself comes from the hostCommands allowlist entry, not
// from args, so PATH lookup is intentionally not performed.
func (r *Runner) runHostCommand(ctx context.Context, path string, args []string) uint8 {
	cmd := exec.CommandContext(ctx, path, args[1:]...)
	cmd.Dir = r.Dir
	cmd.Env = r.filterHostEnv()
	if r.stdin != nil {
		cmd.Stdin = r.stdin
	}
	cmd.Stdout = r.stdout
	cmd.Stderr = r.stderr

	err := cmd.Run()
	// If the runner's context expired (MaxExecutionTime, parent cancel,
	// or builtin-style cooperative cancel), exec.CommandContext kills
	// the child with SIGKILL and cmd.Run returns *exec.ExitError with
	// ExitCode() == -1. We must surface ctx.Err() back through Run()
	// rather than mapping the signal to a numeric exit code, otherwise
	// Run() returns ExitStatus(130) and the CLI's timeout path
	// (context.DeadlineExceeded → "execution timed out", exit 124)
	// never fires and run-span telemetry is misclassified as success.
	// Use exit.fatal so the err is recorded; the returned uint8 is only
	// observed when r.exit.err is nil, so the value is symbolic here.
	if ctxErr := ctx.Err(); ctxErr != nil {
		r.exit.fatal(ctxErr)
		return 130
	}
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// ExitCode returns -1 if the process was terminated by a signal
		// without a context cancel having fired (e.g. an external
		// SIGKILL from outside this process). Map that to 130 — the
		// shell-conventional "terminated" code — so the caller can still
		// observe a non-zero exit.
		code := exitErr.ExitCode()
		if code < 0 {
			return 130
		}
		return uint8(code)
	}
	// Failure to start or some other I/O error: surface to stderr and
	// return 127 (the shell convention for "command not found / not
	// executable").
	r.errf("rshell: %s: %v\n", args[0], err)
	return 127
}

// filterHostEnv builds a minimal env slice for host binaries from the
// runner's environment overlay (r.writeEnv) — NOT the ambient Go
// process env — forwarding only the names in hostEnvAllowlist that
// are also marked Exported. Matches bash semantics: a script-level
// assignment like `PATH=/tmp; hostcmd` does not propagate to the
// child unless PATH was previously exported (via interp.Env or an
// `export` statement). Inline command assignments
// (`PATH=/safe hostcmd`) propagate because call() forces
// vr.Exported = true before dispatch.
//
// Reading from r.writeEnv is what makes the runner's documented
// "empty by default, no host env inherited" guarantee hold for host
// binaries: an unset PATH/HOME/LANG in the runner is simply omitted,
// regardless of what the surrounding Go process exports.
func (r *Runner) filterHostEnv() []string {
	out := make([]string, 0, len(hostEnvAllowlist))
	for _, name := range hostEnvAllowlist {
		vr := r.writeEnv.Get(name)
		if !vr.Declared() || !vr.Exported {
			continue
		}
		out = append(out, name+"="+vr.String())
	}
	return out
}
