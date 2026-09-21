// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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
func TestElevatedRedirectNotAllowedForNonElevatableCommand(t *testing.T) {
	dir := t.TempDir()
	restrictedDir := filepath.Join(dir, "restricted")
	require.NoError(t, os.Mkdir(restrictedDir, 0000))
	t.Cleanup(func() { os.Chmod(restrictedDir, 0755) }) //nolint:errcheck
	target := filepath.Join(restrictedDir, "out.txt")

	var elevateCalls int
	var stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, &stderr, &stderr),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		AllowedCommands([]string{"rshell:echo", "rshell:cat"}),
		// Only echo is elevatable; cat is allowed but not elevatable.
		SelectiveElevation([]string{"rshell:echo"}, chmodElevator(t, restrictedDir, &elevateCalls)),
	)
	require.NoError(t, err)
	defer runner.Close()

	// The redirect target sits inside a directory that is only accessible
	// while elevated. Since "cat" is not in the elevatable-commands policy,
	// elevatedRedirectCommand must not recognize this statement, so the
	// redirect's open() runs unprivileged and fails with exit 1 — the
	// command itself never even runs to hit the separate "elevation not
	// allowed" (126) check in call(). The important assertion is that
	// elevate() is never called and the file is never created.
	program, err := ParseScript("sudo cat "+target+" > "+target, "")
	require.NoError(t, err)
	err = runner.Run(context.Background(), program)
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	require.Equal(t, ExitStatus(1), status)
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

// TestElevatedRedirectDynamicMarkerNotElevated documents the deliberate
// limitation: only a fully static "sudo <literal-name>" form is recognized
// for elevated redirect opening. A dynamically expanded marker still opens
// its redirect unprivileged, exactly like before this change.
func TestElevatedRedirectDynamicMarkerNotElevated(t *testing.T) {
	dir := t.TempDir()
	restrictedDir := filepath.Join(dir, "restricted")
	require.NoError(t, os.Mkdir(restrictedDir, 0000))
	t.Cleanup(func() { os.Chmod(restrictedDir, 0755) }) //nolint:errcheck
	target := filepath.Join(restrictedDir, "out.txt")

	var elevateCalls int
	var stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, os.Stdout, &stderr),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		AllowedCommands([]string{"rshell:echo"}),
		SelectiveElevation([]string{"rshell:echo"}, chmodElevator(t, restrictedDir, &elevateCalls)),
	)
	require.NoError(t, err)
	defer runner.Close()

	// The redirect target sits inside a directory that is only accessible
	// while elevated. Since elevatedRedirectCommand only recognizes a
	// static literal "sudo", the redirect's open() runs unprivileged here
	// and fails with exit 1, without ever calling elevate().
	program, err := ParseScript(`marker=sudo; $marker echo hi > `+target, "")
	require.NoError(t, err)
	err = runner.Run(context.Background(), program)
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	require.Equal(t, ExitStatus(1), status)
	require.Equal(t, 0, elevateCalls)
	_, statErr := os.Stat(target)
	require.Error(t, statErr, "dynamic sudo marker must not elevate the redirect open")
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
