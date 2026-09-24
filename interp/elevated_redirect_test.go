// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// chmodElevator returns an ElevateFunc that simulates a privilege boundary by
// making restrictedDir accessible only for the duration of run(), then
// restoring the original mode. It records every call in *calls so tests can
// assert whether elevation happened.
func chmodElevator(t *testing.T, restrictedDir string, calls *int) ElevateFunc {
	t.Helper()
	return func(_ context.Context, _ string, run func()) error {
		*calls++
		if err := os.Chmod(restrictedDir, 0755); err != nil {
			return err
		}
		defer os.Chmod(restrictedDir, 0000) //nolint:errcheck
		run()
		return nil
	}
}

// TestElevatedRedirectOpensAtElevatedPrivilege is a regression test for the
// privileged-helper gap where a redirect's file-open always ran at the
// runner's unprivileged effective UID, even for a "sudo <elevatable-command>"
// statement. It simulates the privilege boundary with directory permissions:
// a target directory that is inaccessible unless the elevate callback is
// running proves the redirect's open() happened inside the elevated window.
func TestElevatedRedirectOpensAtElevatedPrivilege(t *testing.T) {
	dir := t.TempDir()
	restrictedDir := filepath.Join(dir, "restricted")
	require.NoError(t, os.Mkdir(restrictedDir, 0000))
	t.Cleanup(func() { os.Chmod(restrictedDir, 0755) }) //nolint:errcheck
	target := filepath.Join(restrictedDir, "out.txt")

	var elevateCalls int
	runner, err := New(
		StdIO(nil, os.Stdout, os.Stderr),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		AllowedCommands([]string{"rshell:echo"}),
		SelectiveElevation([]string{"rshell:echo"}, chmodElevator(t, restrictedDir, &elevateCalls)),
	)
	require.NoError(t, err)
	defer runner.Close()

	program, err := ParseScript("sudo echo hi > "+target, "")
	require.NoError(t, err)
	require.NoError(t, runner.Run(context.Background(), program))

	// One elevation for the redirect open, one for the command dispatch.
	require.Equal(t, 2, elevateCalls)

	require.NoError(t, os.Chmod(restrictedDir, 0755))
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "hi\n", string(data))
}

// TestElevatedRedirectAppend covers >> alongside > for the same elevated
// open path.
func TestElevatedRedirectAppend(t *testing.T) {
	dir := t.TempDir()
	restrictedDir := filepath.Join(dir, "restricted")
	require.NoError(t, os.Mkdir(restrictedDir, 0000))
	t.Cleanup(func() { os.Chmod(restrictedDir, 0755) }) //nolint:errcheck
	target := filepath.Join(restrictedDir, "out.txt")

	// Pre-create the file while temporarily accessible, then re-restrict.
	require.NoError(t, os.Chmod(restrictedDir, 0755))
	require.NoError(t, os.WriteFile(target, []byte("first\n"), 0644))
	require.NoError(t, os.Chmod(restrictedDir, 0000))

	var elevateCalls int
	runner, err := New(
		StdIO(nil, os.Stdout, os.Stderr),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		AllowedCommands([]string{"rshell:echo"}),
		SelectiveElevation([]string{"rshell:echo"}, chmodElevator(t, restrictedDir, &elevateCalls)),
	)
	require.NoError(t, err)
	defer runner.Close()

	program, err := ParseScript("sudo echo second >> "+target, "")
	require.NoError(t, err)
	require.NoError(t, runner.Run(context.Background(), program))
	require.Equal(t, 2, elevateCalls)

	require.NoError(t, os.Chmod(restrictedDir, 0755))
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "first\nsecond\n", string(data))
}

// TestElevatedRedirectNotAllowedForNonElevatableCommand ensures the redirect
// is never opened elevated for a "sudo" command that is not present in the
// elevatable-commands policy. Without this, a script could create a
// root-owned file as a side effect of a sudo call that call() ultimately
// rejects with exit 126.
//
// call() authorizes a command (including whether "sudo" on it is allowed)
// before setup() ever applies that statement's redirects (see (*Runner).call
// and (*Runner).callExpr), so a denied elevation is rejected with exit 126
// before the redirect is opened at all — at any privilege level, not merely
// unprivileged. The important assertion is that elevate() is never called
// and the file is never created.
func TestElevatedRedirectNotAllowedForNonElevatableCommand(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")

	var elevateCalls int
	var stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, &stderr, &stderr),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		AllowedCommands([]string{"rshell:echo", "rshell:cat"}),
		// Only echo is elevatable; cat is allowed but not elevatable.
		SelectiveElevation([]string{"rshell:echo"}, func(_ context.Context, _ string, run func()) error {
			elevateCalls++
			run()
			return nil
		}),
	)
	require.NoError(t, err)
	defer runner.Close()

	// The redirect target is within AllowedPaths (an unprivileged "cat >
	// target" would succeed there), which isolates the assertion to the
	// elevation-authorization gate itself rather than sandbox containment.
	program, err := ParseScript("sudo cat "+target+" > "+target, "")
	require.NoError(t, err)
	err = runner.Run(context.Background(), program)
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	require.Equal(t, ExitStatus(126), status)
	require.Contains(t, stderr.String(), "elevation not allowed")
	require.Equal(t, 0, elevateCalls)
	_, statErr := os.Stat(target)
	require.Error(t, statErr, "redirect target must not be created for a denied elevation")
}

// TestElevatedRedirectRejectedInPipeline mirrors the existing pipeline
// restriction on elevated commands: a sudo statement inside a pipeline stage
// must not have its redirect elevated either.
func TestElevatedRedirectRejectedInPipeline(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	var elevateCalls int
	var stdout, stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, &stdout, &stderr),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		AllowedCommands([]string{"rshell:echo", "rshell:cat"}),
		SelectiveElevation([]string{"rshell:echo"}, func(_ context.Context, _ string, run func()) error {
			elevateCalls++
			run()
			return nil
		}),
	)
	require.NoError(t, err)
	defer runner.Close()

	program, err := ParseScript("sudo echo hi > "+target+" | cat", "")
	require.NoError(t, err)
	require.NoError(t, runner.Run(context.Background(), program))
	require.Equal(t, 0, elevateCalls)
	require.Contains(t, stderr.String(), "not allowed in pipelines")
}

// TestElevatedRedirectNotApplicableInReadOnlyMode ensures elevation is never
// consulted for redirect opens in read-only mode: write-target redirects are
// rejected outright there regardless of the sudo marker.
func TestElevatedRedirectNotApplicableInReadOnlyMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	var elevateCalls int
	var stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, os.Stdout, &stderr),
		AllowedPaths([]string{dir + ":rw"}),
		AllowedCommands([]string{"rshell:echo"}),
		SelectiveElevation([]string{"rshell:echo"}, func(_ context.Context, _ string, run func()) error {
			elevateCalls++
			run()
			return nil
		}),
	)
	require.NoError(t, err)
	defer runner.Close()

	// A literal file redirect target is statically rejected at validation
	// time in read-only mode (exit 2), before elevation is ever consulted.
	program, err := ParseScript("sudo echo hi > "+target, "")
	require.NoError(t, err)
	err = runner.Run(context.Background(), program)
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	require.Equal(t, ExitStatus(2), status)
	require.Equal(t, 0, elevateCalls)
	_, statErr := os.Stat(target)
	require.Error(t, statErr, "file should not have been created in read-only mode")
}

// TestElevatedRedirectDynamicMarkerAlsoElevates documents that a dynamically
// expanded "sudo" marker (e.g. through a variable) elevates its own
// write-target redirect exactly like a literal "sudo <name>" would. There is
// no separate static-literal restriction on this path: the elevation
// decision in call() is made after the command word is already fully
// expanded, at the same point call() itself decides whether to elevate the
// command, so both decisions see the same (real, expanded) command name.
// This differs from the read-only-mode static-literal restriction on
// non-"/dev/null" redirect targets, which is a distinct, narrower rule for a
// different purpose (rejecting a blocked redirect before it can run any
// command substitution the target might contain).
func TestElevatedRedirectDynamicMarkerAlsoElevates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not restrict directory access on Windows; the privilege-boundary simulation this test relies on has no effect there")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission bits; the chmod-based privilege-boundary simulation has no effect when the test itself runs as UID 0")
	}
	dir := t.TempDir()
	restrictedDir := filepath.Join(dir, "restricted")
	require.NoError(t, os.Mkdir(restrictedDir, 0000))
	t.Cleanup(func() { os.Chmod(restrictedDir, 0755) }) //nolint:errcheck
	target := filepath.Join(restrictedDir, "out.txt")

	var elevateCalls int
	runner, err := New(
		StdIO(nil, os.Stdout, os.Stderr),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		AllowedCommands([]string{"rshell:echo"}),
		SelectiveElevation([]string{"rshell:echo"}, chmodElevator(t, restrictedDir, &elevateCalls)),
	)
	require.NoError(t, err)
	defer runner.Close()

	program, err := ParseScript(`marker=sudo; $marker echo hi > `+filepath.ToSlash(target), "")
	require.NoError(t, err)
	require.NoError(t, runner.Run(context.Background(), program))

	// One elevation for the redirect open, one for the command dispatch,
	// exactly like the literal-marker case in
	// TestElevatedRedirectOpensAtElevatedPrivilege.
	require.Equal(t, 2, elevateCalls)
	require.NoError(t, os.Chmod(restrictedDir, 0755))
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "hi\n", string(data))
}

// TestElevatedRedirectWithoutSelectiveElevationConfigured ensures the
// no-elevate-callback default behaves exactly as before: the redirect is
// never elevated and "sudo" itself is rejected the same way it always was.
func TestElevatedRedirectWithoutSelectiveElevationConfigured(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	var stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, os.Stdout, &stderr),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		AllowedCommands([]string{"rshell:echo"}),
	)
	require.NoError(t, err)
	defer runner.Close()

	program, err := ParseScript("sudo echo hi > "+target, "")
	require.NoError(t, err)
	err = runner.Run(context.Background(), program)
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	require.Equal(t, ExitStatus(126), status)
	require.Contains(t, stderr.String(), "elevation not allowed")
}

// TestElevatedRedirectCommandSubstitutionInTargetNotElevated is a regression
// test for a privilege-escalation finding: expanding a redirect's target
// word (r.literal(rd.Word)) can run a command substitution, and that
// substituted command must always run at the caller's ordinary privilege —
// never inside the elevate() window used to open a "sudo <elevatable>"
// statement's own redirect. An earlier version of this fix wrapped the
// entire redirect-opening loop (including word expansion) in elevate(),
// which let ANY plain command reachable through $(...) in the redirect
// target run as root, regardless of whether that command was itself
// authorized to elevate.
//
// This is proven with a chmod-based privilege-boundary simulation: a plain
// (non-sudo) "cat" of a file in a directory that is only accessible while
// elevate() is running. If the substitution ran during that window, its
// output ("SECRET") would be prepended into the expanded redirect target
// filename. It must not be: the resulting file must be exactly "out.txt".
//
// Skipped on Windows: unlike the AllowedPaths-based negative tests above,
// this test's whole premise depends on chmod actually restricting access —
// it must prove a plain command CANNOT read a path that is only accessible
// during the elevate() window, so "outside AllowedPaths" cannot substitute
// for the privilege boundary here. os.Chmod(dir, 0000) does not restrict
// access on Windows (NTFS ACLs, not POSIX mode bits), so "cat" would
// unconditionally succeed there regardless of whether the fix under test is
// present, making the assertion meaningless rather than merely weaker.
func TestElevatedRedirectCommandSubstitutionInTargetNotElevated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not restrict directory access on Windows; the privilege-boundary simulation this test relies on has no effect there")
	}
	if os.Getuid() == 0 {
		// Root bypasses POSIX permission bits entirely, so chmod(dir, 0000)
		// does not restrict access when the test process itself runs as
		// UID 0 (e.g. inside a root-run/container test suite): the
		// "unprivileged" cat inside the command substitution would
		// unconditionally succeed and read "SECRET" regardless of whether
		// the fix under test is present, for reasons unrelated to
		// elevation. Matches the identical root-bypass skip already used in
		// builtins/cd/cd_unix_test.go and builtins/tail/tail_unix_test.go.
		t.Skip("root bypasses permission bits; the chmod-based privilege-boundary simulation has no effect when the test itself runs as UID 0")
	}
	dir := t.TempDir()
	restrictedDir := filepath.Join(dir, "restricted")
	require.NoError(t, os.Mkdir(restrictedDir, 0000))
	t.Cleanup(func() { os.Chmod(restrictedDir, 0755) }) //nolint:errcheck
	probeFile := filepath.Join(restrictedDir, "probe.txt")
	require.NoError(t, os.Chmod(restrictedDir, 0755))
	require.NoError(t, os.WriteFile(probeFile, []byte("SECRET"), 0644))
	require.NoError(t, os.Chmod(restrictedDir, 0000))

	target := filepath.Join(dir, "out.txt")
	runner, err := New(
		StdIO(nil, io.Discard, io.Discard),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		AllowedCommands([]string{"rshell:echo", "rshell:cat"}),
		// "cat" is deliberately NOT elevatable — only "echo" is. If the
		// substitution's "cat" ran elevated, it would prove elevation leaked
		// to a command that was never authorized to receive it at all.
		SelectiveElevation([]string{"rshell:echo"}, func(_ context.Context, _ string, run func()) error {
			require.NoError(t, os.Chmod(restrictedDir, 0755))
			defer os.Chmod(restrictedDir, 0000) //nolint:errcheck
			run()
			return nil
		}),
	)
	require.NoError(t, err)
	defer runner.Close()

	// Paths are converted to forward slashes before being embedded in the
	// script: on Windows, filepath.Join produces backslashes, which inside
	// a double-quoted word are backslash-escapes to the shell parser, not
	// literal path separators.
	program, err := ParseScript(`sudo echo hi > "$(cat `+filepath.ToSlash(probeFile)+` 2>/dev/null; echo `+filepath.ToSlash(target)+`)"`, "")
	require.NoError(t, err)
	require.NoError(t, runner.Run(context.Background(), program))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	require.ElementsMatch(t, []string{"restricted", "out.txt"}, names,
		"the command substitution must not have read the restricted probe file into the redirect target name")
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "hi\n", string(data))
}

// TestElevatedRedirectRequiresAllowedCommandNotJustElevatable is a
// regression test for a second finding alongside the one above:
// SelectiveElevation's own doc comment states it does not add a command to
// AllowedCommands, and the privileged-helper policy intersects the
// AllowedCommands and elevatableCommands axes independently. An effective
// configuration can therefore legally name a command that is in
// elevatableCommands but NOT in AllowedCommands. call() already refuses to
// dispatch such a command ("command not allowed", exit 127) before it ever
// reaches the elevatable-commands check — and, in this architecture, before
// setup() ever applies that statement's redirects at all — so its redirect
// must never be elevated either, nor even opened unprivileged: otherwise the
// redirect target could be created or truncated before call() ever rejects
// the command.
func TestElevatedRedirectRequiresAllowedCommandNotJustElevatable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")

	var elevateCalls int
	var stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, os.Stdout, &stderr),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		// "grep" is elevatable below but deliberately absent from
		// AllowedCommands.
		AllowedCommands([]string{"rshell:echo"}),
		SelectiveElevation([]string{"rshell:echo", "rshell:grep"}, func(_ context.Context, _ string, run func()) error {
			elevateCalls++
			run()
			return nil
		}),
	)
	require.NoError(t, err)
	defer runner.Close()

	// The redirect target is within AllowedPaths (an unprivileged
	// "grep x file > target" would succeed there), which isolates the
	// assertion to the command-authorization gate itself rather than
	// sandbox containment. The important assertion is that elevate() is
	// never called and the redirect target is never created.
	program, err := ParseScript("sudo grep x "+target+" > "+target, "")
	require.NoError(t, err)
	err = runner.Run(context.Background(), program)
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	require.Equal(t, ExitStatus(127), status)
	require.Contains(t, stderr.String(), "command not allowed")
	require.Equal(t, 0, elevateCalls)
	_, statErr := os.Stat(target)
	require.Error(t, statErr, "redirect target must not be created for a command that is elevatable but not allowed")
}

// TestElevatedRedirectRequiresRegisteredBuiltin is a regression test for a
// third finding alongside the two above: a name can be both allowed and
// elevatable in the effective policy while still not being a registered
// builtin (a stale or misspelled entry in the operator/backend policy).
// call() itself falls through to the "unknown command" branch for such a
// name (via builtins.Lookup) and never dispatches or elevates it, so a
// redirect must be held to the same requirement — otherwise the redirect
// target could be created or truncated as root for a command that will
// never actually run.
func TestElevatedRedirectRequiresRegisteredBuiltin(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "out.txt")

	var elevateCalls int
	var stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, os.Stdout, &stderr),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		// "missing" is not a registered builtin, but it is both allowed and
		// elevatable — exactly the stale/misspelled-policy-entry scenario.
		AllowedCommands([]string{"rshell:missing"}),
		SelectiveElevation([]string{"rshell:missing"}, func(_ context.Context, _ string, run func()) error {
			elevateCalls++
			run()
			return nil
		}),
	)
	require.NoError(t, err)
	defer runner.Close()

	program, err := ParseScript("sudo missing > "+target, "")
	require.NoError(t, err)
	err = runner.Run(context.Background(), program)
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	require.Equal(t, ExitStatus(1), status)
	require.Equal(t, 0, elevateCalls)
	_, statErr := os.Stat(target)
	require.Error(t, statErr, "redirect target must not be created for a name that is allowed and elevatable but not a registered builtin")
}

// TestElevatedRedirectElevateFailureIsFatalAndReported is a regression test
// for a finding on the redirect-elevation error path: when the elevate()
// callback itself fails (e.g. the privileged worker's setresuid failing)
// before ever running the callback, the failure must reach the caller of
// Run() as a real error — mirroring the existing behavior for an elevated
// command's own dispatch (call()'s identical "elevating %s: %w" fatal path)
// — rather than leaving a bare, unexplained exit 1 with no diagnostic on
// stderr and no error from Run().
func TestElevatedRedirectElevateFailureIsFatalAndReported(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	var stderr bytes.Buffer
	elevateErr := errors.New("boom: setresuid failed")
	runner, err := New(
		StdIO(nil, os.Stdout, &stderr),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		AllowedCommands([]string{"rshell:echo"}),
		SelectiveElevation([]string{"rshell:echo"}, func(_ context.Context, _ string, _ func()) error {
			// Fails without ever invoking run(), simulating a failure to
			// acquire the elevated privilege window in the first place.
			return elevateErr
		}),
	)
	require.NoError(t, err)
	defer runner.Close()

	program, err := ParseScript("sudo echo hi > "+target, "")
	require.NoError(t, err)
	err = runner.Run(context.Background(), program)
	require.Error(t, err)
	require.ErrorIs(t, err, elevateErr, "the underlying elevate() error must be preserved and reachable via errors.Is")
	require.Contains(t, err.Error(), "elevating redirect for echo")
	// Unlike an ordinary sandbox/policy rejection (exit 1, script continues),
	// a malfunctioning elevate() callback is fatal: Run() must return the
	// wrapped error itself, not merely ExitStatus(1).
	var status ExitStatus
	require.False(t, errors.As(err, &status), "an elevate() malfunction must be fatal, not a plain exit status")
	_, statErr := os.Stat(target)
	require.Error(t, statErr, "redirect target must not be created when elevate() itself fails")
}

// mockReadWriteCloser is a minimal io.ReadWriteCloser that records whether
// Close was called, for asserting a caller's cleanup behavior without
// needing a real file descriptor.
type mockReadWriteCloser struct {
	closed bool
}

func (m *mockReadWriteCloser) Read([]byte) (int, error)    { return 0, io.EOF }
func (m *mockReadWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (m *mockReadWriteCloser) Close() error {
	m.closed = true
	return nil
}

// TestWithElevatedRedirectOpenClosesFileWhenElevateFailsAfterOpen is a
// regression test for a file-descriptor leak: if an ElevateFunc invokes
// run() (so fn's underlying open(2) succeeds) and then itself returns a
// non-nil error — for example because it failed to restore privileges on
// its way out, which its documented contract requires even when run fails
// — the already-opened file must still be closed rather than discarded.
// Discarding it would leak one descriptor per attempted elevated redirect.
//
// This calls (*Runner).withElevatedRedirectOpen directly with a mock
// io.ReadWriteCloser so the test can assert Close was actually called on
// the exact value fn returned, rather than inferring it indirectly.
func TestWithElevatedRedirectOpenClosesFileWhenElevateFailsAfterOpen(t *testing.T) {
	elevateErr := errors.New("boom: failed to restore privileges")
	mock := &mockReadWriteCloser{}
	runner, err := New(
		StdIO(nil, io.Discard, io.Discard),
		WithMode(ModeRemediation),
		AllowedCommands([]string{"rshell:echo"}),
		SelectiveElevation([]string{"rshell:echo"}, func(_ context.Context, _ string, run func()) error {
			run()
			return elevateErr
		}),
	)
	require.NoError(t, err)
	defer runner.Close()
	runner.pendingElevatedRedirect = "echo"

	f, err := runner.withElevatedRedirectOpen(context.Background(), func() (io.ReadWriteCloser, error) {
		return mock, nil
	})
	require.Nil(t, f, "the caller must not receive a value it could double-close or otherwise use after the elevate() failure")
	require.Error(t, err)
	require.ErrorIs(t, err, elevateErr)
	require.True(t, mock.closed, "the opened file must be closed, not leaked, when elevate() reports failure after run() already opened it")
}

// TestElevatedRedirectStderrDoesNotLeakIntoCommandSubstitution is a
// regression test for a privilege-escalation finding: the elevated writer
// installed for a "sudo <name>" statement's own redirect is assigned to
// r.stdout/r.stderr before r.cmd expands that same statement's command
// arguments. A command substitution nested in a later word (e.g.
// "sudo echo \"$(echo injected >&2)\" 2>target") runs inside a subshell
// created via (*Runner).subshell, which used to copy r.stderr by plain
// value — so the substitution's own PLAIN (non-sudo, unauthorized-to-
// elevate) command inherited the SAME already-open, already-elevated file
// descriptor and could write through it directly. A Unix write(2) only
// checks the permissions the fd was opened with, never the calling code's
// current privilege, so inheriting the descriptor was equivalent to hand-
// ing that unauthorized command root write access to the target.
//
// The fix wraps an elevated writer in *elevatedWriter (carrying its own
// pre-elevation fallback) and has subshell() unwrap it back to that
// fallback, so a nested runner never inherits the elevated descriptor
// itself, regardless of how the interface value reached r.stdout/r.stderr.
//
// This is verified with a chmod-based privilege-boundary simulation:
// "echo injected >&2" inside the substitution is a plain command that must
// never be able to write into a target that is only accessible during
// elevate(). It is confirmed empty afterward, while the outer "sudo echo"
// command's own output (verified separately by
// TestElevatedRedirectOpensAtElevatedPrivilege) is unaffected by the fix.
//
// Skipped on Windows/root for the same reasons as
// TestElevatedRedirectCommandSubstitutionInTargetNotElevated: the whole
// premise depends on chmod actually restricting access, which does not hold
// on Windows (NTFS ACLs) or when the test process itself runs as UID 0
// (POSIX permission bits do not apply to root).
func TestElevatedRedirectStderrDoesNotLeakIntoCommandSubstitution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not restrict directory access on Windows; the privilege-boundary simulation this test relies on has no effect there")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission bits; the chmod-based privilege-boundary simulation has no effect when the test itself runs as UID 0")
	}
	dir := t.TempDir()
	restrictedDir := filepath.Join(dir, "restricted")
	require.NoError(t, os.Mkdir(restrictedDir, 0000))
	t.Cleanup(func() { os.Chmod(restrictedDir, 0755) }) //nolint:errcheck
	target := filepath.Join(restrictedDir, "out.txt")

	var elevateCalls int
	runner, err := New(
		StdIO(nil, io.Discard, io.Discard),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		AllowedCommands([]string{"rshell:echo"}),
		SelectiveElevation([]string{"rshell:echo"}, chmodElevator(t, restrictedDir, &elevateCalls)),
	)
	require.NoError(t, err)
	defer runner.Close()

	program, err := ParseScript(`sudo echo "$(echo injected >&2)" 2>`+filepath.ToSlash(target), "")
	require.NoError(t, err)
	require.NoError(t, runner.Run(context.Background(), program))

	require.NoError(t, os.Chmod(restrictedDir, 0755))
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Empty(t, string(data), "the command substitution's plain 'echo >&2' must not have written into the elevated redirect target via inherited stderr")
}

// TestElevatedRedirectStackedElevatedRedirectsFullyUnwrap is a regression
// test for a follow-up finding on the same fix: a statement with two
// write-target redirects onto the same fd within the same elevated
// statement (e.g. two 2> redirects) nests *elevatedWriter values, because
// each redirect's fallback is captured as the field's current value
// immediately before installing a new wrapper — so the second wrapper's
// fallback is the first *elevatedWriter, not the pre-statement original. A
// single unwrap (as originally implemented) would still hand a nested
// runner an *elevatedWriter for the first redirect's target, through which
// it could still write. unwrapElevatedWriter must follow the fallback chain
// until it reaches a writer that is no longer wrapped.
//
// Skipped on Windows/root for the same chmod-privilege-boundary reasons as
// TestElevatedRedirectCommandSubstitutionInTargetNotElevated.
func TestElevatedRedirectStackedElevatedRedirectsFullyUnwrap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not restrict directory access on Windows; the privilege-boundary simulation this test relies on has no effect there")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission bits; the chmod-based privilege-boundary simulation has no effect when the test itself runs as UID 0")
	}
	dir := t.TempDir()
	restrictedA := filepath.Join(dir, "restrictedA")
	restrictedB := filepath.Join(dir, "restrictedB")
	for _, d := range []string{restrictedA, restrictedB} {
		require.NoError(t, os.Mkdir(d, 0000))
		d := d
		t.Cleanup(func() { os.Chmod(d, 0755) }) //nolint:errcheck
	}
	targetA := filepath.Join(restrictedA, "a.txt")
	targetB := filepath.Join(restrictedB, "b.txt")

	var elevateCalls int
	runner, err := New(
		StdIO(nil, os.Stdout, os.Stderr),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		AllowedCommands([]string{"rshell:echo"}),
		SelectiveElevation([]string{"rshell:echo"}, func(_ context.Context, _ string, run func()) error {
			elevateCalls++
			require.NoError(t, os.Chmod(restrictedA, 0755))
			require.NoError(t, os.Chmod(restrictedB, 0755))
			defer os.Chmod(restrictedA, 0000) //nolint:errcheck
			defer os.Chmod(restrictedB, 0000) //nolint:errcheck
			run()
			return nil
		}),
	)
	require.NoError(t, err)
	defer runner.Close()

	// Two stderr redirects on the same statement: 2>a then 2>b. b's fallback
	// (installed second) is the *elevatedWriter for a, not the
	// pre-statement original. The nested "echo injected >&2" substitution
	// must not be able to write through EITHER.
	program, err := ParseScript(`sudo echo "$(echo injected >&2)" 2>`+filepath.ToSlash(targetA)+` 2>`+filepath.ToSlash(targetB), "")
	require.NoError(t, err)
	require.NoError(t, runner.Run(context.Background(), program))

	require.NoError(t, os.Chmod(restrictedA, 0755))
	require.NoError(t, os.Chmod(restrictedB, 0755))
	dataA, err := os.ReadFile(targetA)
	require.NoError(t, err)
	dataB, err := os.ReadFile(targetB)
	require.NoError(t, err)
	require.Empty(t, string(dataA), "the nested substitution must not write through the first stacked elevated redirect")
	require.Empty(t, string(dataB), "the nested substitution must not write through the second stacked elevated redirect")
}

// TestElevatedRedirectCatShortcutDoesNotLeakIntoElevatedStderr is a
// regression test for a follow-up finding on the same class of bug: the
// $(<file) shortcut in cmdSubst runs directly on the parent Runner (it never
// executes a command, so it never creates a subshell() copy), and prints its
// own diagnostics (a disallowed-cat message, or an r.open failure) via
// r.errf directly to r.stderr. If r.stderr is currently an *elevatedWriter
// installed for the enclosing statement's own "sudo <name>" redirect, that
// diagnostic — which the shortcut substitution itself was never authorized
// to elevate — would otherwise be written through the elevated descriptor.
//
// Skipped on Windows/root for the same chmod-privilege-boundary reasons as
// TestElevatedRedirectCommandSubstitutionInTargetNotElevated.
func TestElevatedRedirectCatShortcutDoesNotLeakIntoElevatedStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not restrict directory access on Windows; the privilege-boundary simulation this test relies on has no effect there")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission bits; the chmod-based privilege-boundary simulation has no effect when the test itself runs as UID 0")
	}
	dir := t.TempDir()
	restrictedDir := filepath.Join(dir, "restricted")
	require.NoError(t, os.Mkdir(restrictedDir, 0000))
	t.Cleanup(func() { os.Chmod(restrictedDir, 0755) }) //nolint:errcheck
	target := filepath.Join(restrictedDir, "out.txt")

	var elevateCalls int
	runner, err := New(
		StdIO(nil, os.Stdout, os.Stderr),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		// "cat" is deliberately not in AllowedCommands, so $(<missing) hits
		// the "not permitted" errf branch rather than the r.open branch.
		AllowedCommands([]string{"rshell:true"}),
		SelectiveElevation([]string{"rshell:true"}, chmodElevator(t, restrictedDir, &elevateCalls)),
	)
	require.NoError(t, err)
	defer runner.Close()

	program, err := ParseScript(`sudo true "$(<missing)" 2>`+filepath.ToSlash(target), "")
	require.NoError(t, err)
	require.NoError(t, runner.Run(context.Background(), program))

	require.NoError(t, os.Chmod(restrictedDir, 0755))
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Empty(t, string(data), "the cat-shortcut's disallowed-command diagnostic must not have been written into the elevated redirect target")
}

// TestElevatedRedirectLaterRedirectSetupErrorDoesNotLeakIntoEarlierElevatedTarget
// is a regression test for a follow-up finding on the same class of bug:
// once an earlier redirect on a statement has installed the elevated
// writer (e.g. "2>>/allowed/log"), a LATER redirect's own setup diagnostic
// on that same statement — which can embed that later redirect's expanded,
// potentially attacker-influenced target path, including embedded
// newlines — must not be written through the already-elevated descriptor
// merely because r.stderr currently happens to be it. The elevated
// descriptor's content must be restricted to exactly what the authorized
// command itself writes through its own CallContext, not any interpreter-
// level diagnostic about a different, unrelated redirect.
//
// Fixed by making (*Runner).errf and (*Runner).expandErr — the
// interpreter's own diagnostic channel, never a builtin's real output —
// always unwrap a currently-installed *elevatedWriter before writing.
func TestElevatedRedirectLaterRedirectSetupErrorDoesNotLeakIntoEarlierElevatedTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not restrict directory access on Windows; the privilege-boundary simulation this test relies on has no effect there")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission bits; the chmod-based privilege-boundary simulation has no effect when the test itself runs as UID 0")
	}
	dir := t.TempDir()
	restrictedDir := filepath.Join(dir, "restricted")
	require.NoError(t, os.Mkdir(restrictedDir, 0000))
	t.Cleanup(func() { os.Chmod(restrictedDir, 0755) }) //nolint:errcheck
	logTarget := filepath.Join(restrictedDir, "log.txt")

	// The second redirect's target is outside every AllowedPaths root, so
	// its own setup fails with a sandbox permission error that embeds this
	// exact (here: benign, but in principle attacker-influenced) path text.
	outsideDir := t.TempDir()
	secondTarget := filepath.Join(outsideDir, "attacker-controlled-path")

	var elevateCalls int
	runner, err := New(
		StdIO(nil, os.Stdout, os.Stderr),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		AllowedCommands([]string{"rshell:true"}),
		SelectiveElevation([]string{"rshell:true"}, chmodElevator(t, restrictedDir, &elevateCalls)),
	)
	require.NoError(t, err)
	defer runner.Close()

	// First redirect (2>>logTarget) is elevated and installs the
	// elevatedWriter on r.stderr. Second redirect (>secondTarget) fails
	// (outside AllowedPaths); its setup diagnostic must go to the ordinary
	// pre-elevation stderr, not through the already-elevated log target.
	program, err := ParseScript("sudo true 2>>"+filepath.ToSlash(logTarget)+" >"+filepath.ToSlash(secondTarget), "")
	require.NoError(t, err)
	_ = runner.Run(context.Background(), program)

	require.NoError(t, os.Chmod(restrictedDir, 0755))
	data, err := os.ReadFile(logTarget)
	require.NoError(t, err)
	require.Empty(t, string(data), "the second redirect's own setup-failure diagnostic must not have been written into the first (elevated) redirect's target")
}

// TestElevatedRedirectRejectedInsidePipelineStageAndItsSubshells is the
// redirect-elevation analogue of
// TestSelectiveElevationRejectsMarkerInSubshellPipelineStage
// (selective_elevation_test.go), which covers elevated COMMAND dispatch
// inside a pipeline stage (including nested explicit subshells). This test
// covers the same set of positions for an elevated statement's own
// write-target REDIRECT: a pipeline stage runs concurrently with its
// siblings while sharing the same process-wide effective UID, and an
// explicit (…) subshell resets inPipeline (a distinct, telemetry-only
// flag — see its doc comment) but must not reset the authorization gate
// (inPipelineStage) that keeps elevation refused there.
//
// Verified with the same chmod-based privilege-boundary simulation used
// elsewhere in this file: the redirect target sits in a directory that is
// only accessible while elevate() is running, so a wrongly authorized
// elevation would create the file; refused elevation leaves it uncreated.
func TestElevatedRedirectRejectedInsidePipelineStageAndItsSubshells(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not restrict directory access on Windows; the privilege-boundary simulation this test relies on has no effect there")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission bits; the chmod-based privilege-boundary simulation has no effect when the test itself runs as UID 0")
	}
	dir := t.TempDir()
	restrictedDir := filepath.Join(dir, "restricted")
	require.NoError(t, os.Mkdir(restrictedDir, 0000))
	t.Cleanup(func() { os.Chmod(restrictedDir, 0755) }) //nolint:errcheck
	target := filepath.Join(restrictedDir, "out.txt")

	for _, script := range []string{
		"sudo echo elevated > " + filepath.ToSlash(target) + " | cat",
		"cat | sudo echo elevated > " + filepath.ToSlash(target),
		"(sudo echo elevated > " + filepath.ToSlash(target) + ") | cat",
		"( (sudo echo elevated > " + filepath.ToSlash(target) + ") ) | cat",
		"{ (sudo echo elevated > " + filepath.ToSlash(target) + "); } | cat",
	} {
		t.Run(script, func(t *testing.T) {
			var elevateCalls int
			var stderr bytes.Buffer
			runner, err := New(
				StdIO(nil, os.Stdout, &stderr),
				WithMode(ModeRemediation),
				AllowedPaths([]string{dir + ":rw"}),
				AllowedCommands([]string{"rshell:echo", "rshell:cat"}),
				SelectiveElevation([]string{"rshell:echo"}, chmodElevator(t, restrictedDir, &elevateCalls)),
			)
			require.NoError(t, err)
			defer runner.Close()

			program, err := ParseScript(script, "")
			require.NoError(t, err)
			// A pipeline's exit status is whichever side's own exit status
			// the shell reports for it (the right-hand stage's, per POSIX);
			// when the refused "elevated commands are not allowed in
			// pipelines" statement is itself that side, Run() returns its
			// ExitStatus(126) rather than nil. Either way, the two
			// assertions below are what actually matter here.
			_ = runner.Run(context.Background(), program)

			require.Equal(t, 0, elevateCalls, "elevate() must never be called for a redirect inside a pipeline stage")
			require.Contains(t, stderr.String(), "not allowed in pipelines")
			require.NoError(t, os.Chmod(restrictedDir, 0755))
			_, statErr := os.Stat(target)
			require.Error(t, statErr, "redirect target must not be created when elevation is correctly refused inside a pipeline stage")
			require.NoError(t, os.Chmod(restrictedDir, 0000))
		})
	}
}
