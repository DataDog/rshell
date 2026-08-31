// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

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
	"os/exec"
	"os/user"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/privilegedhelper"
	"mvdan.cc/sh/v3/syntax"
)

const systemdListenFD = 3
const maxHelperOutputBytes = 256 << 10

func runPrivilegedHelper(ctx context.Context, args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("privileged-helper", flag.ContinueOnError)
	flags.SetOutput(stderr)
	policyPath := systemdPolicyPath()
	flags.StringVar(&policyPath, "policy", policyPath, "optional path to the root-provisioned authorization policy")
	flags.StringVar(&policyPath, "credential", policyPath, "deprecated alias for --policy")
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
	credential, err := loadOptionalPolicy(policyPath)
	if err != nil {
		fmt.Fprintf(stderr, "loading policy: %v\n", err)
		return 1
	}
	account, err := user.Lookup(*userName)
	if err != nil {
		fmt.Fprintf(stderr, "looking up %s: %v\n", *userName, err)
		return 1
	}
	credentials, err := resolveAccountCredentials(account)
	if err != nil {
		fmt.Fprintf(stderr, "resolving credentials for %s: %v\n", *userName, err)
		return 1
	}
	listener, err := inheritedListener()
	if err != nil {
		fmt.Fprintf(stderr, "loading systemd socket: %v\n", err)
		return 1
	}
	defer listener.Close()
	if err := setProcessGroups(credentials.primaryGID, credentials.supplementaryGIDs); err != nil {
		fmt.Fprintf(stderr, "dropping group privileges: %v\n", err)
		return 1
	}
	if err := setEffectiveUID(credentials.uid); err != nil {
		fmt.Fprintf(stderr, "dropping privileges: %v\n", err)
		return 1
	}
	if os.Geteuid() != credentials.uid {
		fmt.Fprintln(stderr, "failed to confirm unprivileged effective uid")
		return 1
	}

	executor := &helperExecutor{}
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

func systemdPolicyPath() string {
	dir := os.Getenv("CREDENTIALS_DIRECTORY")
	if dir == "" {
		return ""
	}
	return dir + "/rshell-policy.json"
}

func loadOptionalPolicy(path string) (*privilegedhelper.Credential, error) {
	if path == "" {
		return nil, nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// systemd supplies an empty fallback policy when the optional source
	// file is absent.
	if info.Size() == 0 {
		return nil, nil
	}
	return privilegedhelper.LoadCredential(path)
}

type accountCredentials struct {
	uid               int
	primaryGID        int
	supplementaryGIDs []int
}

func resolveAccountCredentials(account *user.User) (accountCredentials, error) {
	groupIDs, err := account.GroupIds()
	if err != nil {
		return accountCredentials{}, fmt.Errorf("list groups: %w", err)
	}
	return parseAccountCredentials(account.Uid, account.Gid, groupIDs)
}

func parseAccountCredentials(uidValue, primaryGIDValue string, groupIDValues []string) (accountCredentials, error) {
	uid, err := parseAccountID("uid", uidValue)
	if err != nil {
		return accountCredentials{}, err
	}
	if uid == 0 {
		return accountCredentials{}, errors.New("uid must be non-zero")
	}
	primaryGID, err := parseAccountID("primary gid", primaryGIDValue)
	if err != nil {
		return accountCredentials{}, err
	}
	supplementaryGIDs := make([]int, 0, len(groupIDValues))
	seen := make(map[int]struct{}, len(groupIDValues))
	for _, value := range groupIDValues {
		gid, parseErr := parseAccountID("supplementary gid", value)
		if parseErr != nil {
			return accountCredentials{}, parseErr
		}
		if gid == primaryGID {
			continue
		}
		if _, exists := seen[gid]; exists {
			continue
		}
		seen[gid] = struct{}{}
		supplementaryGIDs = append(supplementaryGIDs, gid)
	}
	sort.Ints(supplementaryGIDs)
	return accountCredentials{uid: uid, primaryGID: primaryGID, supplementaryGIDs: supplementaryGIDs}, nil
}

func parseAccountID(kind, value string) (int, error) {
	const reservedLinuxID = uint64(1<<32 - 1)
	id, err := strconv.ParseUint(value, 10, 32)
	if err != nil || id == reservedLinuxID || uint64(int(id)) != id {
		return 0, fmt.Errorf("invalid %s %q", kind, value)
	}
	return int(id), nil
}

func setProcessGroups(primaryGID int, supplementaryGIDs []int) error {
	if err := setSupplementaryGroups(supplementaryGIDs); err != nil {
		return fmt.Errorf("set supplementary groups: %w", err)
	}
	_, _, errno := syscall.AllThreadsSyscall(syscall.SYS_SETRESGID, uintptr(primaryGID), uintptr(primaryGID), uintptr(primaryGID))
	if errno != 0 {
		return fmt.Errorf("set primary gid: %w", errno)
	}
	if os.Getgid() != primaryGID || os.Getegid() != primaryGID {
		return errors.New("failed to confirm primary gid")
	}
	actualSupplementaryGIDs, err := syscall.Getgroups()
	if err != nil {
		return fmt.Errorf("confirm supplementary groups: %w", err)
	}
	sort.Ints(actualSupplementaryGIDs)
	wantSupplementaryGIDs := slices.Clone(supplementaryGIDs)
	sort.Ints(wantSupplementaryGIDs)
	if !slices.Equal(actualSupplementaryGIDs, wantSupplementaryGIDs) {
		return fmt.Errorf("failed to confirm supplementary groups: got %v, want %v", actualSupplementaryGIDs, wantSupplementaryGIDs)
	}
	return nil
}

// setSupplementaryGroups updates every Go-managed OS thread. Like UID/GID
// changes, supplementary groups are per-thread Linux credentials even though
// POSIX presents them as process-wide state. AllThreadsSyscall also fails with
// ENOTSUP for cgo-linked binaries, which is preferable to changing only one
// thread; the production helper is deliberately built with CGO_ENABLED=0.
func setSupplementaryGroups(gids []int) error {
	rawGIDs := make([]uint32, len(gids))
	for index, gid := range gids {
		rawGIDs[index] = uint32(gid)
	}
	var groups uintptr
	if len(rawGIDs) != 0 {
		groups = uintptr(unsafe.Pointer(&rawGIDs[0]))
	}
	_, _, errno := syscall.AllThreadsSyscall(syscall.SYS_SETGROUPS, uintptr(len(rawGIDs)), groups, 0)
	runtime.KeepAlive(rawGIDs)
	if errno != 0 {
		return errno
	}
	return nil
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

type workerCommandFactory func(context.Context) (*exec.Cmd, error)

type helperExecutor struct {
	newWorker workerCommandFactory
}

func (e *helperExecutor) Execute(ctx context.Context, command *privilegedhelper.VerifiedCommand) (*privilegedhelper.ExecuteResponse, error) {
	if command == nil {
		return nil, errors.New("verified command is required")
	}
	var input bytes.Buffer
	if err := writeWorkerMessage(&input, workerRequest{Version: workerProtocolVersion, Command: command}); err != nil {
		return nil, fmt.Errorf("encode privileged worker request: %w", err)
	}
	newWorker := e.newWorker
	if newWorker == nil {
		newWorker = newPrivilegedWorkerCommand
	}
	worker, err := newWorker(ctx)
	if err != nil {
		return nil, err
	}
	worker.Stdin = &input
	worker.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	var stdout, stderr boundedBuffer
	stdout.limit = maxWorkerFrameBytes
	stderr.limit = maxWorkerStderrBytes
	worker.Stdout = &stdout
	worker.Stderr = &stderr
	if err := worker.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		detail := strings.TrimSpace(stderr.String())
		if stderr.truncated {
			detail += " (truncated)"
		}
		if detail == "" {
			return nil, fmt.Errorf("privileged worker failed: %w", err)
		}
		return nil, fmt.Errorf("privileged worker failed: %w: %s", err, detail)
	}
	if stdout.truncated {
		return nil, errors.New("privileged worker response exceeds the size limit")
	}
	var response workerResponse
	if err := readWorkerMessage(bytes.NewReader(stdout.Bytes()), &response); err != nil {
		return nil, fmt.Errorf("decode privileged worker response: %w", err)
	}
	if response.Version != workerProtocolVersion {
		return nil, fmt.Errorf("unsupported privileged worker response version %d", response.Version)
	}
	if response.Error != "" {
		return nil, errors.New(response.Error)
	}
	if response.Result == nil {
		return nil, errors.New("privileged worker returned no result")
	}
	return response.Result, nil
}

func newPrivilegedWorkerCommand(ctx context.Context) (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate privileged worker executable: %w", err)
	}
	return exec.CommandContext(ctx, executable, "privileged-worker"), nil
}

func executeVerifiedCommand(ctx context.Context, command *privilegedhelper.VerifiedCommand, unprivilegedUID int) (*privilegedhelper.ExecuteResponse, error) {
	program, err := interp.ParseScript(command.Command, "")
	if err != nil {
		return nil, fmt.Errorf("parse signed command: %w", err)
	}
	if err := rejectElevatedPipelines(program); err != nil {
		return nil, err
	}
	if err := applyWorkerSandbox(command); err != nil {
		return nil, fmt.Errorf("apply privileged worker sandbox: %w", err)
	}
	var stdout, stderr boundedBuffer
	runner, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interp.WarningsWriter(io.Discard),
		interp.AllowedPaths(command.AllowedPaths),
		interp.AllowedCommands(command.AllowedCommands),
		interp.WithMode(interp.ModeRemediation),
		interp.SelectiveElevation(command.ElevatableCommands, (&workerElevator{unprivilegedUID: unprivilegedUID}).elevate),
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
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	originalLen := len(p)
	limit := b.limit
	if limit <= 0 {
		limit = maxHelperOutputBytes
	}
	remaining := limit - b.Len()
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

type workerElevator struct{ unprivilegedUID int }

func (e *workerElevator) elevate(_ context.Context, _ string, run func()) error {
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
