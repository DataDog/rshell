// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.

//go:build !linux

package main

import (
	"context"
	"fmt"
	"io"
)

func runPrivilegedHelper(_ context.Context, _ []string, stderr io.Writer) int {
	fmt.Fprintln(stderr, "privileged-helper is supported only on Linux")
	return 1
}
