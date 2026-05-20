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
	"github.com/DataDog/rshell/builtins/cd"
	continuecmd "github.com/DataDog/rshell/builtins/continue"
	"github.com/DataDog/rshell/builtins/cut"
	"github.com/DataDog/rshell/builtins/df"
	"github.com/DataDog/rshell/builtins/du"
	"github.com/DataDog/rshell/builtins/echo"
	"github.com/DataDog/rshell/builtins/exit"
	falsecmd "github.com/DataDog/rshell/builtins/false"
	"github.com/DataDog/rshell/builtins/find"
	"github.com/DataDog/rshell/builtins/grep"
	"github.com/DataDog/rshell/builtins/head"
	"github.com/DataDog/rshell/builtins/help"
	"github.com/DataDog/rshell/builtins/ip"
	killcmd "github.com/DataDog/rshell/builtins/kill"
	"github.com/DataDog/rshell/builtins/logrotate"
	"github.com/DataDog/rshell/builtins/ls"
	"github.com/DataDog/rshell/builtins/ping"
	printfcmd "github.com/DataDog/rshell/builtins/printf"
	pscmd "github.com/DataDog/rshell/builtins/ps"
	"github.com/DataDog/rshell/builtins/pwd"
	readcmd "github.com/DataDog/rshell/builtins/read"
	"github.com/DataDog/rshell/builtins/sed"
	sortcmd "github.com/DataDog/rshell/builtins/sort"
	"github.com/DataDog/rshell/builtins/ss"
	"github.com/DataDog/rshell/builtins/strings_cmd"
	"github.com/DataDog/rshell/builtins/systemctl"
	"github.com/DataDog/rshell/builtins/tail"
	"github.com/DataDog/rshell/builtins/tee"
	"github.com/DataDog/rshell/builtins/testcmd"
	"github.com/DataDog/rshell/builtins/tr"
	truecmd "github.com/DataDog/rshell/builtins/true"
	"github.com/DataDog/rshell/builtins/truncate"
	"github.com/DataDog/rshell/builtins/uname"
	"github.com/DataDog/rshell/builtins/uniq"
	"github.com/DataDog/rshell/builtins/wc"
	"github.com/DataDog/rshell/builtins/write_file"
	"github.com/DataDog/rshell/builtins/xargs"
)

var registerOnce sync.Once

func registerBuiltins() {
	registerOnce.Do(func() {
		for _, cmd := range []builtins.Command{
			breakcmd.Cmd,
			cat.Cmd,
			cd.Cmd,
			cut.Cmd,
			continuecmd.Cmd,
			df.Cmd,
			du.Cmd,
			echo.Cmd,
			exit.Cmd,
			falsecmd.Cmd,
			find.Cmd,
			grep.Cmd,
			head.Cmd,
			help.Cmd,
			ip.Cmd,
			killcmd.Cmd,
			ls.Cmd,
			logrotate.Cmd,
			ping.Cmd,
			sortcmd.Cmd,
			printfcmd.Cmd,
			pscmd.Cmd,
			pwd.Cmd,
			readcmd.Cmd,
			sed.Cmd,
			ss.Cmd,
			strings_cmd.Cmd,
			systemctl.Cmd,
			tail.Cmd,
			tee.Cmd,
			testcmd.Cmd,
			testcmd.BracketCmd,
			truncate.Cmd,
			tr.Cmd,
			truecmd.Cmd,
			uname.Cmd,
			uniq.Cmd,
			wc.Cmd,
			write_file.Cmd,
			xargs.Cmd,
		} {
			cmd.Register()
		}
	})
}
