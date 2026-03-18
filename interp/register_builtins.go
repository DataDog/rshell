// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"sync"

	"github.com/DataDog/rshell/builtins"
	breakcmd "github.com/DataDog/rshell/builtins/break"
	"github.com/DataDog/rshell/builtins/cat"
	continuecmd "github.com/DataDog/rshell/builtins/continue"
	"github.com/DataDog/rshell/builtins/cut"
	"github.com/DataDog/rshell/builtins/echo"
	"github.com/DataDog/rshell/builtins/exit"
	falsecmd "github.com/DataDog/rshell/builtins/false"
	"github.com/DataDog/rshell/builtins/find"
	"github.com/DataDog/rshell/builtins/grep"
	"github.com/DataDog/rshell/builtins/head"
	"github.com/DataDog/rshell/builtins/help"
	"github.com/DataDog/rshell/builtins/ip"
	"github.com/DataDog/rshell/builtins/ls"
	"github.com/DataDog/rshell/builtins/ping"
	printfcmd "github.com/DataDog/rshell/builtins/printf"
	"github.com/DataDog/rshell/builtins/sed"
	sortcmd "github.com/DataDog/rshell/builtins/sort"
	"github.com/DataDog/rshell/builtins/ss"
	"github.com/DataDog/rshell/builtins/strings_cmd"
	"github.com/DataDog/rshell/builtins/tail"
	"github.com/DataDog/rshell/builtins/testcmd"
	"github.com/DataDog/rshell/builtins/tr"
	truecmd "github.com/DataDog/rshell/builtins/true"
	"github.com/DataDog/rshell/builtins/uniq"
	"github.com/DataDog/rshell/builtins/wc"
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
			help.Cmd,
			ip.Cmd,
			ls.Cmd,
			ping.Cmd,
			sortcmd.Cmd,
			printfcmd.Cmd,
			sed.Cmd,
			ss.Cmd,
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
