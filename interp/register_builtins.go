// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"sync"

	"github.com/DataDog/rshell/interp/builtins"
	breakcmd "github.com/DataDog/rshell/interp/builtins/break"
	"github.com/DataDog/rshell/interp/builtins/cat"
	continuecmd "github.com/DataDog/rshell/interp/builtins/continue"
	"github.com/DataDog/rshell/interp/builtins/echo"
	"github.com/DataDog/rshell/interp/builtins/exit"
	falsecmd "github.com/DataDog/rshell/interp/builtins/false"
	"github.com/DataDog/rshell/interp/builtins/head"
	"github.com/DataDog/rshell/interp/builtins/tail"
	"github.com/DataDog/rshell/interp/builtins/testcmd"
	truecmd "github.com/DataDog/rshell/interp/builtins/true"
	"github.com/DataDog/rshell/interp/builtins/uniq"
	"github.com/DataDog/rshell/interp/builtins/wc"
)

var registerOnce sync.Once

func registerBuiltins() {
	registerOnce.Do(func() {
		for _, cmd := range []builtins.Command{
			breakcmd.Cmd,
			cat.Cmd,
			continuecmd.Cmd,
			echo.Cmd,
			exit.Cmd,
			falsecmd.Cmd,
			head.Cmd,
			tail.Cmd,
			testcmd.Cmd,
			testcmd.BracketCmd,
			truecmd.Cmd,
			uniq.Cmd,
			wc.Cmd,
		} {
			cmd.Register()
		}
	})
}
