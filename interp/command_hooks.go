// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"context"

	"github.com/DataDog/rshell/builtins"
)

// CommandHooks are passive callbacks invoked around rshell command dispatch
// when configured by [WithCommandHooks]. Hooks are observability only: they
// cannot authorize, deny, rewrite, or otherwise alter command execution.
type CommandHooks struct {
	After func(context.Context, CommandEvent)
}

// CommandEvent describes one command dispatch observed by rshell.
type CommandEvent struct {
	Name string
	Args []string

	IsAllowed bool
	IsKnown   bool
	ExitCode  uint8
}

// WithCommandHooks installs passive command-dispatch hooks. Nil callbacks are
// ignored.
func WithCommandHooks(hooks CommandHooks) RunnerOption {
	return func(r *Runner) error {
		r.commandHooks = hooks
		return nil
	}
}

func (r *Runner) commandHooksEnabled() bool {
	return r.commandHooks.After != nil
}

func (r *Runner) callCommandHook(ctx context.Context, hook func(context.Context, CommandEvent), event CommandEvent) {
	if hook == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	hook(ctx, event)
}

func (r *Runner) notifyCommandDenied(ctx context.Context, name string, args []string) {
	if !r.commandHooksEnabled() {
		return
	}
	r.callCommandHook(ctx, r.commandHooks.After, CommandEvent{
		Name:      name,
		Args:      append([]string(nil), args...),
		IsAllowed: false,
		IsKnown:   commandIsKnown(name),
		ExitCode:  127,
	})
}

func commandIsKnown(name string) bool {
	_, ok := builtins.Lookup(name)
	return ok
}
