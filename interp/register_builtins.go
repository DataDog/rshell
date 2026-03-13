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
	"github.com/DataDog/rshell/interp/builtins/cut"
	"github.com/DataDog/rshell/interp/builtins/echo"
	"github.com/DataDog/rshell/interp/builtins/exit"
	falsecmd "github.com/DataDog/rshell/interp/builtins/false"
	"github.com/DataDog/rshell/interp/builtins/find"
	"github.com/DataDog/rshell/interp/builtins/grep"
	"github.com/DataDog/rshell/interp/builtins/head"
	"github.com/DataDog/rshell/interp/builtins/ls"
	printfcmd "github.com/DataDog/rshell/interp/builtins/printf"
	"github.com/DataDog/rshell/interp/builtins/sed"
	sortcmd "github.com/DataDog/rshell/interp/builtins/sort"
	"github.com/DataDog/rshell/interp/builtins/strings_cmd"
	"github.com/DataDog/rshell/interp/builtins/tail"
	"github.com/DataDog/rshell/interp/builtins/testcmd"
	"github.com/DataDog/rshell/interp/builtins/tr"
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
			cut.Cmd,
			continuecmd.Cmd,
			echo.Cmd,
			exit.Cmd,
			falsecmd.Cmd,
			find.Cmd,
			grep.Cmd,
			head.Cmd,
			ls.Cmd,
			sortcmd.Cmd,
			printfcmd.Cmd,
			sed.Cmd,
			strings_cmd.Cmd,
			tail.Cmd,
			testcmd.Cmd,
			testcmd.BracketCmd,
			tr.Cmd,
			truecmd.Cmd,
			uniq.Cmd,
			wc.Cmd,
		} {
			cmd.Register()
		}
	})
}
