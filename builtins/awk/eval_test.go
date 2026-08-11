// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins"
)

func requireCommandPipeNextAction(t *testing.T, rt *runtime, pipe *commandPipe, future stmtFuture) commandPipeAction {
	t.Helper()
	action, err := rt.commandPipeNextAction(pipe, future)
	require.NoError(t, err)
	return action
}

func TestPrependStmtFutureBorrowsStatementSegments(t *testing.T) {
	const statementCount = 1024
	stmts := make([]stmt, statementCount)
	for i := range stmts {
		stmts[i] = &exprStmt{}
	}
	future := stmtFuture{stmts: []stmt{&exprStmt{}}}

	for i := 1; i < len(stmts); i++ {
		remaining := prependStmtFuture(stmts[i:], &future)
		if &remaining.stmts[0] != &stmts[i] {
			t.Fatalf("statement tail at index %d was copied", i)
		}
		if remaining.next != &future {
			t.Fatalf("future at index %d was flattened into the statement tail", i)
		}
	}
}

func TestCommandPipeNextActionFollowsStmtFutureOrder(t *testing.T) {
	const command = "cat"
	closeStmt := &exprStmt{x: &callExpr{
		name: "close",
		args: []expr{&stringExpr{value: command}},
	}}
	closeFuture := stmtFuture{stmts: []stmt{closeStmt}}
	writeFuture := prependStmtFuture([]stmt{&printStmt{
		pipe: &stringExpr{value: command},
	}}, &closeFuture)
	rt := newRuntime(&builtins.CallContext{}, &program{})
	pipe := &commandPipe{command: command}

	require.Equal(t, commandPipeActionWrite, requireCommandPipeNextAction(t, rt, pipe, writeFuture))
	require.Equal(t, commandPipeActionClose, requireCommandPipeNextAction(t, rt, pipe, closeFuture))
}

func TestRuleFutureUsesConstantSizeCursor(t *testing.T) {
	const ruleCount = 1024
	prog := &program{rules: make([]rule, ruleCount)}
	for i := range prog.rules {
		prog.rules[i] = rule{kind: ruleNormal, action: []stmt{&exprStmt{}}}
	}
	rt := newRuntime(&builtins.CallContext{}, prog)

	for i := range prog.rules {
		future := rt.ruleFuture(ruleNormal, i+1)
		if len(future.stmts) != 0 || future.next != nil {
			t.Fatalf("rule future at index %d materialized statement segments", i)
		}
		if future.rules == nil || future.rules.kind != ruleNormal || future.rules.nextRule != i+1 {
			t.Fatalf("rule future at index %d does not retain its cursor", i)
		}
	}
}

func TestRuleFuturePreservesPhaseOrder(t *testing.T) {
	const command = "cat"
	write := func() stmt {
		return &printStmt{pipe: &stringExpr{value: command}}
	}
	close := func() stmt {
		return &exprStmt{x: &callExpr{
			name: "close",
			args: []expr{&stringExpr{value: command}},
		}}
	}
	prog := &program{rules: []rule{
		{kind: ruleBegin, action: []stmt{&exprStmt{}}},
		{kind: ruleNormal, action: []stmt{write()}},
		{kind: ruleEnd, action: []stmt{write()}},
		{kind: ruleBegin, action: []stmt{close()}},
		{kind: ruleNormal, action: []stmt{close()}},
		{kind: ruleEnd, action: []stmt{close()}},
	}}
	rt := newRuntime(&builtins.CallContext{}, prog)
	pipe := &commandPipe{command: command}

	tests := []struct {
		name     string
		kind     ruleKind
		nextRule int
		want     commandPipeAction
	}{
		{name: "remaining BEGIN before main", kind: ruleBegin, nextRule: 1, want: commandPipeActionClose},
		{name: "main before END", kind: ruleBegin, nextRule: 4, want: commandPipeActionWrite},
		{name: "current record before next record", kind: ruleNormal, nextRule: 2, want: commandPipeActionClose},
		{name: "next record before END", kind: ruleNormal, nextRule: 5, want: commandPipeActionWrite},
		{name: "remaining END", kind: ruleEnd, nextRule: 3, want: commandPipeActionClose},
		{name: "nothing after END", kind: ruleEnd, nextRule: len(prog.rules), want: commandPipeActionNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			future := rt.ruleFuture(tc.kind, tc.nextRule)
			require.Equal(t, tc.want, requireCommandPipeNextAction(t, rt, pipe, future))
		})
	}
}

func TestCommandPipeCachesStatementSuffixes(t *testing.T) {
	const (
		command        = "cat"
		statementCount = 1024
	)
	stmts := make([]stmt, statementCount)
	for i := range stmts {
		stmts[i] = &exprStmt{}
	}
	stmts[len(stmts)-1] = &exprStmt{x: &callExpr{
		name: "close",
		args: []expr{&stringExpr{value: command}},
	}}
	rt := newRuntime(&builtins.CallContext{}, &program{})
	pipe := &commandPipe{command: command}

	require.Equal(t, commandPipeActionClose, requireCommandPipeNextAction(t, rt, pipe, stmtFuture{stmts: stmts}))
	require.Len(t, pipe.lookahead.stmtSuffixes, len(stmts))
	wantUsage := commandPipeLookaheadUsage{stmtSuffixes: len(stmts)}
	require.Equal(t, wantUsage, pipe.lookahead.usage)
	require.Equal(t, wantUsage, rt.lookaheadUsage)

	stmts[len(stmts)-1] = &printStmt{pipe: &stringExpr{value: command}}
	for i := range stmts {
		if action := requireCommandPipeNextAction(t, rt, pipe, stmtFuture{stmts: stmts[i:]}); action != commandPipeActionClose {
			t.Fatalf("statement suffix %d was rescanned after being cached", i)
		}
	}
	require.Len(t, pipe.lookahead.stmtSuffixes, len(stmts))
	require.Equal(t, wantUsage, pipe.lookahead.usage)
	require.Equal(t, wantUsage, rt.lookaheadUsage)
}

func TestCommandPipeCachesRecursiveFunctionTouches(t *testing.T) {
	const command = "cat"
	call := func(name string) stmt {
		return &exprStmt{x: &callExpr{name: name}}
	}
	prog := &program{functions: map[string]*functionDef{
		"a": {
			name: "a",
			body: []stmt{
				call("b"),
				&exprStmt{x: &callExpr{
					name: "close",
					args: []expr{&stringExpr{value: command}},
				}},
			},
		},
		"b": {name: "b", body: []stmt{call("a")}},
	}}
	rt := newRuntime(&builtins.CallContext{}, prog)
	pipe := &commandPipe{command: command}
	firstCall := []stmt{call("b")}
	secondCall := []stmt{call("b")}

	require.NotEqual(t, commandPipeActionNone, requireCommandPipeNextAction(t, rt, pipe, stmtFuture{stmts: firstCall}))
	require.Equal(t, map[string]bool{"a": true, "b": true}, pipe.lookahead.functionTouches)
	wantUsage := commandPipeLookaheadUsage{stmtSuffixes: 1, functionTouches: 2}
	require.Equal(t, wantUsage, pipe.lookahead.usage)
	require.Equal(t, wantUsage, rt.lookaheadUsage)

	prog.functions["a"].body = []stmt{call("b")}
	require.NotEqual(t, commandPipeActionNone, requireCommandPipeNextAction(t, rt, pipe, stmtFuture{stmts: secondCall}))
	require.Equal(t, commandPipeActionNone, requireCommandPipeNextAction(t, rt,
		&commandPipe{command: command},
		stmtFuture{stmts: secondCall},
	))
}

func TestCommandPipeCachesRuleSuffixesUntilClose(t *testing.T) {
	const (
		command   = "cat"
		ruleCount = 1024
	)
	prog := &program{rules: make([]rule, ruleCount)}
	for i := range prog.rules {
		prog.rules[i] = rule{kind: ruleNormal, action: []stmt{&exprStmt{}}}
	}
	prog.rules[len(prog.rules)-1].action[0] = &exprStmt{x: &callExpr{
		name: "close",
		args: []expr{&stringExpr{value: command}},
	}}
	callCtx := &builtins.CallContext{
		RunScriptWithStdin: func(context.Context, string, string, io.Reader, io.Writer) (uint8, error) {
			return 0, nil
		},
	}
	rt := newRuntime(callCtx, prog)
	pipe, err := rt.commandPipe(command)
	require.NoError(t, err)

	require.Equal(t, commandPipeActionClose, requireCommandPipeNextAction(t, rt, pipe, rt.ruleFuture(ruleNormal, 0)))
	require.Len(t, pipe.lookahead.ruleSuffixes[ruleNormal], len(prog.rules)+1)
	require.Len(t, pipe.lookahead.stmtSuffixes, len(prog.rules))
	wantUsage := commandPipeLookaheadUsage{
		stmtSuffixes: len(prog.rules),
		ruleSuffixes: len(prog.rules) + 1,
	}
	require.Equal(t, wantUsage, pipe.lookahead.usage)
	require.Equal(t, wantUsage, rt.lookaheadUsage)

	prog.rules[len(prog.rules)-1].action[0] = &printStmt{pipe: &stringExpr{value: command}}
	for i := range prog.rules {
		if action := requireCommandPipeNextAction(t, rt, pipe, rt.ruleFuture(ruleNormal, i)); action != commandPipeActionClose {
			t.Fatalf("rule suffix %d was rescanned after being cached", i)
		}
	}

	_, ok, err := rt.closeCommandPipe(context.Background(), command, false)
	require.NoError(t, err)
	require.True(t, ok)
	require.Nil(t, pipe.lookahead.stmtSuffixes)
	require.Nil(t, pipe.lookahead.functionTouches)
	for _, suffixes := range pipe.lookahead.ruleSuffixes {
		require.Nil(t, suffixes)
	}
	require.Zero(t, pipe.lookahead.usage)
	require.Zero(t, rt.lookaheadUsage)
}

func TestCommandPipeLookaheadAggregateLimitReleasesOnClose(t *testing.T) {
	const (
		firstCommand  = "cat"
		secondCommand = "sort"
	)
	callCtx := &builtins.CallContext{
		Stdout: io.Discard,
		Stderr: io.Discard,
		RunScriptWithStdin: func(context.Context, string, string, io.Reader, io.Writer) (uint8, error) {
			return 0, nil
		},
	}
	rt := newRuntime(callCtx, &program{})
	rt.lookaheadLimits.stmtSuffixes = 2
	stmts := []stmt{&exprStmt{}, &exprStmt{}}

	firstPipe, err := rt.commandPipe(firstCommand)
	require.NoError(t, err)
	rt.lookaheadLimits.ruleSuffixes = 0
	err = rt.reserveCommandPipeLookahead(firstPipe, commandPipeLookaheadUsage{
		stmtSuffixes:    1,
		ruleSuffixes:    1,
		functionTouches: 1,
	})
	require.EqualError(t, err, "command pipe rule suffix lookahead cache exceeds 0 entries")
	require.Zero(t, firstPipe.lookahead.usage)
	require.Zero(t, rt.lookaheadUsage)
	rt.lookaheadLimits.ruleSuffixes = maxCommandPipeRuleSuffixEntries

	require.Equal(t, commandPipeActionNone, requireCommandPipeNextAction(t, rt, firstPipe, stmtFuture{stmts: stmts}))
	require.Equal(t, commandPipeLookaheadUsage{stmtSuffixes: 2}, rt.lookaheadUsage)

	secondPipe, err := rt.commandPipe(secondCommand)
	require.NoError(t, err)
	err = rt.writeStdoutString(context.Background(), "held", stmtFuture{stmts: stmts[1:]})
	require.EqualError(t, err, "command pipe statement suffix lookahead cache exceeds 2 entries")
	require.Zero(t, rt.stdoutBuf.Len())
	require.Nil(t, secondPipe.lookahead.stmtSuffixes)
	require.Zero(t, secondPipe.lookahead.usage)
	require.Equal(t, commandPipeLookaheadUsage{stmtSuffixes: 2}, rt.lookaheadUsage)

	_, closed, err := rt.closeCommandPipe(context.Background(), firstCommand, false)
	require.NoError(t, err)
	require.True(t, closed)
	require.Zero(t, firstPipe.lookahead.usage)
	require.Zero(t, rt.lookaheadUsage)

	reopened, err := rt.commandPipe(firstCommand)
	require.NoError(t, err)
	require.Equal(t, commandPipeActionNone, requireCommandPipeNextAction(t, rt, reopened, stmtFuture{stmts: stmts[1:]}))
	require.Equal(t, commandPipeLookaheadUsage{stmtSuffixes: 1}, rt.lookaheadUsage)
}

func TestRuntimeClearsCommandPipeLookaheadAfterEarlyError(t *testing.T) {
	const command = "cat"
	var stderr bytes.Buffer
	prog := &program{rules: []rule{{
		kind: ruleBegin,
		action: []stmt{
			&printStmt{args: []expr{&stringExpr{value: "pipe"}}, pipe: &stringExpr{value: command}},
			&printStmt{args: []expr{&stringExpr{value: "stdout"}}},
			&exprStmt{x: &callExpr{name: "missing"}},
			&printStmt{args: []expr{&stringExpr{value: "future"}}, pipe: &stringExpr{value: command}},
		},
	}}}
	rt := newRuntime(&builtins.CallContext{
		Stdout: io.Discard,
		Stderr: &stderr,
	}, prog)

	result := rt.run(context.Background(), nil)
	require.Equal(t, uint8(1), result.Code)
	require.Contains(t, stderr.String(), `function "missing" not defined`)
	require.Zero(t, rt.lookaheadUsage)
	pipe := rt.pipes[command]
	require.NotNil(t, pipe)
	require.Nil(t, pipe.lookahead.stmtSuffixes)
	require.Nil(t, pipe.lookahead.functionTouches)
}
