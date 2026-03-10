// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package echo_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGNUCompatEchoPlain — plain echo outputs its arguments.
//
// Bash command: echo hello world
// Expected:     "hello world\n"
func TestGNUCompatEchoPlain(t *testing.T) {
	stdout, _, code := runScript(t, "echo hello world")
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}

// TestGNUCompatEchoNoArgs — echo with no arguments outputs a newline.
//
// Bash command: echo
// Expected:     "\n"
func TestGNUCompatEchoNoArgs(t *testing.T) {
	stdout, _, code := runScript(t, "echo")
	assert.Equal(t, 0, code)
	assert.Equal(t, "\n", stdout)
}

// TestGNUCompatEchoN — -n suppresses trailing newline.
//
// Bash command: echo -n hello
// Expected:     "hello"
func TestGNUCompatEchoN(t *testing.T) {
	stdout, _, code := runScript(t, "echo -n hello")
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello", stdout)
}

// TestGNUCompatEchoNNoArgs — -n with no arguments produces empty output.
//
// Bash command: echo -n
// Expected:     ""
func TestGNUCompatEchoNNoArgs(t *testing.T) {
	stdout, _, code := runScript(t, "echo -n")
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

// TestGNUCompatEchoENewline — -e interprets \n.
//
// Bash command: echo -e 'hello\nworld'
// Expected:     "hello\nworld\n"
func TestGNUCompatEchoENewline(t *testing.T) {
	stdout, _, code := runScript(t, `echo -e 'hello\nworld'`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\nworld\n", stdout)
}

// TestGNUCompatEchoETab — -e interprets \t.
//
// Bash command: echo -e 'a\tb'
// Expected:     "a\tb\n"
func TestGNUCompatEchoETab(t *testing.T) {
	stdout, _, code := runScript(t, `echo -e 'a\tb'`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\tb\n", stdout)
}

// TestGNUCompatEchoEBackslash — -e interprets \\.
//
// Bash command: echo -e 'a\\b'
// Expected:     "a\\b\n"
func TestGNUCompatEchoEBackslash(t *testing.T) {
	stdout, _, code := runScript(t, `echo -e 'a\\b'`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\\b\n", stdout)
}

// TestGNUCompatEchoEC — -e with \c suppresses further output.
//
// Bash command: echo -e 'hello\cworld'
// Expected:     "hello"
func TestGNUCompatEchoEC(t *testing.T) {
	stdout, _, code := runScript(t, `echo -e 'hello\cworld'`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello", stdout)
}

// TestGNUCompatEchoEOctal — -e interprets \0nnn as octal.
//
// Bash command: echo -e '\0101'
// Expected:     "A\n"
func TestGNUCompatEchoEOctal(t *testing.T) {
	stdout, _, code := runScript(t, `echo -e '\0101'`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "A\n", stdout)
}

// TestGNUCompatEchoEHex — -e interprets \xHH as hexadecimal.
//
// Bash command: echo -e '\x41'
// Expected:     "A\n"
func TestGNUCompatEchoEHex(t *testing.T) {
	stdout, _, code := runScript(t, `echo -e '\x41'`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "A\n", stdout)
}

// TestGNUCompatEchoEEscape — -e interprets \e as escape character.
//
// Bash command: echo -e '\e'
// Expected:     "\x1b\n"
func TestGNUCompatEchoEEscape(t *testing.T) {
	stdout, _, code := runScript(t, `echo -e '\e'`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\x1b\n", stdout)
}

// TestGNUCompatEchoEUpperE — -E disables escape interpretation.
//
// Bash command: echo -E 'hello\nworld'
// Expected:     "hello\\nworld\n"
func TestGNUCompatEchoEUpperE(t *testing.T) {
	stdout, _, code := runScript(t, `echo -E 'hello\nworld'`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\\nworld\n", stdout)
}

// TestGNUCompatEchoNE — -ne combines both flags.
//
// Bash command: echo -ne 'hello\nworld'
// Expected:     "hello\nworld"
func TestGNUCompatEchoNE(t *testing.T) {
	stdout, _, code := runScript(t, `echo -ne 'hello\nworld'`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\nworld", stdout)
}

// TestGNUCompatEchoDoubleDash — -- is printed literally.
//
// Bash command: echo -- hello
// Expected:     "-- hello\n"
func TestGNUCompatEchoDoubleDash(t *testing.T) {
	stdout, _, code := runScript(t, "echo -- hello")
	assert.Equal(t, 0, code)
	assert.Equal(t, "-- hello\n", stdout)
}

// TestGNUCompatEchoHelp — --help is printed literally.
//
// Bash command: echo --help
// Expected:     "--help\n"
func TestGNUCompatEchoHelp(t *testing.T) {
	stdout, _, code := runScript(t, "echo --help")
	assert.Equal(t, 0, code)
	assert.Equal(t, "--help\n", stdout)
}

// TestGNUCompatEchoInvalidFlagLiteral — invalid flag combo is literal text.
//
// Bash command: echo -nxyz hello
// Expected:     "-nxyz hello\n"
func TestGNUCompatEchoInvalidFlagLiteral(t *testing.T) {
	stdout, _, code := runScript(t, "echo -nxyz hello")
	assert.Equal(t, 0, code)
	assert.Equal(t, "-nxyz hello\n", stdout)
}

// TestGNUCompatEchoELastWins — last of -e/-E in combined flag wins.
//
// Bash command: echo -eE 'hello\nworld'
// Expected:     "hello\\nworld\n"
func TestGNUCompatEchoELastWins(t *testing.T) {
	stdout, _, code := runScript(t, `echo -eE 'hello\nworld'`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\\nworld\n", stdout)
}

// TestGNUCompatEchoUnicode4 — -e interprets \uHHHH.
//
// Bash command: echo -e '\u0041'
// Expected:     "A\n"
func TestGNUCompatEchoUnicode4(t *testing.T) {
	stdout, _, code := runScript(t, `echo -e '\u0041'`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "A\n", stdout)
}

// TestGNUCompatEchoUnicode8 — -e interprets \UHHHHHHHH.
//
// Bash command: echo -e '\U00000041'
// Expected:     "A\n"
func TestGNUCompatEchoUnicode8(t *testing.T) {
	stdout, _, code := runScript(t, `echo -e '\U00000041'`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "A\n", stdout)
}
