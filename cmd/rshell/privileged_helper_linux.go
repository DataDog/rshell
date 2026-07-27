// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.

//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"strconv"
	"syscall"
	"time"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/privilegedhelper"
	"mvdan.cc/sh/v3/syntax"
)

const systemdListenFD = 3
const maxHelperOutputBytes = 256 << 10

func runPrivilegedHelper(ctx context.Context, args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("privileged-helper", flag.ContinueOnError)
	flags.SetOutput(stderr)
	credentialPath := flags.String("credential", systemdCredentialPath(), "path to the root-provisioned verification credential")
	idleTimeout := flags.Duration("idle-timeout", 30*time.Second, "exit after this long without a connection")
	userName := flags.String("user", "dd-agent", "unprivileged effective user")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "privileged-helper does not accept positional arguments")
		return 2
	}
	if os.Getuid() != 0 {
		fmt.Fprintln(stderr, "privileged-helper requires real uid 0")
		return 1
	}
	if *credentialPath == "" {
		fmt.Fprintln(stderr, "privileged-helper requires a verification credential")
		return 1
	}
	credential, err := privilegedhelper.LoadCredential(*credentialPath)
	if err != nil {
		fmt.Fprintf(stderr, "loading credential: %v\n", err)
		return 1
	}
	account, err := user.Lookup(*userName)
	if err != nil {
		fmt.Fprintf(stderr, "looking up %s: %v\n", *userName, err)
		return 1
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil || uid <= 0 {
		fmt.Fprintf(stderr, "invalid uid for %s\n", *userName)
		return 1
	}
	listener, err := inheritedListener()
	if err != nil {
		fmt.Fprintf(stderr, "loading systemd socket: %v\n", err)
		return 1
	}
	defer listener.Close()
	if err := setEffectiveUID(uid); err != nil {
		fmt.Fprintf(stderr, "dropping privileges: %v\n", err)
		return 1
	}
	if os.Geteuid() != uid {
		fmt.Fprintln(stderr, "failed to confirm unprivileged effective uid")
		return 1
	}

	executor := &helperExecutor{unprivilegedUID: uid}
	server := privilegedhelper.Server{
		Credential:  credential,
		Executor:    executor,
		IdleTimeout: *idleTimeout,
		LogWriter:   stderr,
	}
	if err := server.Serve(ctx, listener); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "serving privileged helper: %v\n", err)
		return 1
	}
	return 0
}

func systemdCredentialPath() string {
	dir := os.Getenv("CREDENTIALS_DIRECTORY")
	if dir == "" {
		return ""
	}
	return dir + "/rshell-verification.json"
}

func inheritedListener() (net.Listener, error) {
	if os.Getenv("LISTEN_PID") != strconv.Itoa(os.Getpid()) || os.Getenv("LISTEN_FDS") != "1" {
		return nil, errors.New("expected exactly one systemd-activated socket")
	}
	file := os.NewFile(systemdListenFD, "rshell-privileged.socket")
	if file == nil {
		return nil, errors.New("systemd socket fd is unavailable")
	}
	defer file.Close()
	listener, err := net.FileListener(file)
	if err != nil {
		return nil, err
	}
	return listener, nil
}

type helperExecutor struct{ unprivilegedUID int }

func (e *helperExecutor) Execute(ctx context.Context, command *privilegedhelper.VerifiedCommand) (*privilegedhelper.ExecuteResponse, error) {
	program, err := interp.ParseScript(command.Command, "")
	if err != nil {
		return nil, fmt.Errorf("parse signed command: %w", err)
	}
	if err := rejectElevatedPipelines(program); err != nil {
		return nil, err
	}
	var stdout, stderr boundedBuffer
	runner, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interp.WarningsWriter(io.Discard),
		interp.AllowedPaths(command.AllowedPaths),
		interp.AllowedCommands(command.AllowedCommands),
		interp.WithMode(interp.ModeRemediation),
		interp.SelectiveElevation(command.ElevatableCommands, e.elevate),
	)
	if err != nil {
		return nil, fmt.Errorf("create privileged runner: %w", err)
	}
	defer runner.Close()
	runErr := runner.Run(ctx, program)
	exitCode := 0
	if runErr != nil {
		var status interp.ExitStatus
		if errors.As(runErr, &status) {
			exitCode = int(status)
		} else {
			return nil, runErr
		}
	}
	warnings := runner.Warnings()
	if stdout.truncated || stderr.truncated {
		warnings = append(warnings, "privileged helper output was truncated")
	}
	return &privilegedhelper.ExecuteResponse{ExitCode: exitCode, Stdout: stdout.String(), Stderr: stderr.String(), SandboxWarnings: warnings}, nil
}

type boundedBuffer struct {
	bytes.Buffer
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	originalLen := len(p)
	remaining := maxHelperOutputBytes - b.Len()
	if remaining <= 0 {
		b.truncated = b.truncated || originalLen > 0
		return originalLen, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, err := b.Buffer.Write(p)
	return originalLen, err
}

func rejectElevatedPipelines(program *syntax.File) error {
	hasSudo, hasPipeline := false, false
	syntax.Walk(program, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.BinaryCmd:
			if node.Op == syntax.Pipe || node.Op == syntax.PipeAll {
				hasPipeline = true
			}
		case *syntax.CallExpr:
			if len(node.Args) > 0 && len(node.Args[0].Parts) == 1 {
				if lit, ok := node.Args[0].Parts[0].(*syntax.Lit); ok && lit.Value == "sudo" {
					hasSudo = true
				}
			}
		}
		return true
	})
	if hasSudo && hasPipeline {
		return errors.New("pipelines are not supported in scripts containing elevated commands")
	}
	return nil
}

func (e *helperExecutor) elevate(_ context.Context, _ string, run func()) error {
	if os.Getuid() != 0 || os.Geteuid() != e.unprivilegedUID {
		return errors.New("helper privilege state is invalid")
	}
	if err := setEffectiveUID(0); err != nil {
		return err
	}
	defer func() {
		restoreErr := setEffectiveUID(e.unprivilegedUID)
		if restoreErr != nil || os.Geteuid() != e.unprivilegedUID {
			// Continuing after a failed privilege drop would turn the next
			// ordinary command into a root command. Fail-stop the helper.
			panic("rshell privileged helper failed to restore its effective uid")
		}
	}()
	if os.Geteuid() != 0 {
		return errors.New("failed to confirm elevated effective uid")
	}
	run()
	return nil
}

// setEffectiveUID updates every Go-managed OS thread. syscall.Seteuid cannot
// be used here: in a cgo-linked binary it delegates to libc, whose setresuid
// call changes only the calling Linux thread. That could leave runtime threads
// at different privilege levels and let the elevated callback migrate onto an
// unprivileged thread (or, more seriously, leave a thread privileged after the
// initial drop).
func setEffectiveUID(uid int) error {
	const unchanged = ^uintptr(0)
	_, _, errno := syscall.AllThreadsSyscall(syscall.SYS_SETRESUID, unchanged, uintptr(uid), unchanged)
	if errno != 0 {
		return errno
	}
	return nil
}
