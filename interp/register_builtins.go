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
	truecmd "github.com/DataDog/rshell/interp/builtins/true"
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
			truecmd.Cmd,
		} {
			builtins.Register(cmd.Name, cmd.Run)
		}
	})
}
