// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"fmt"
	"os"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// validateNode walks the AST and rejects shell constructs that are not
// supported in the safe-shell interpreter.  It is called before execution
// so that disallowed features are caught early with a clear error message.
func validateNode(node syntax.Node) error {
	var err error
	hdocWords := make(map[*syntax.Word]bool)
	syntax.Walk(node, func(n syntax.Node) bool {
		if err != nil {
			return false
		}
		switch n := n.(type) {
		// Blocked expression-level nodes.
		case *syntax.ArithmExp:
			err = fmt.Errorf("arithmetic expansion is not supported")
			return false
		case *syntax.CmdSubst:
			err = fmt.Errorf("command substitution is not supported")
			return false
		case *syntax.ProcSubst:
			err = fmt.Errorf("process substitution is not supported")
			return false
		case *syntax.ParamExp:
			err = validateParamExp(n)
			if err != nil {
				return false
			}
		case *syntax.Assign:
			err = validateAssign(n)
			if err != nil {
				return false
			}

		// Blocked command-level nodes.
		case *syntax.WhileClause:
			err = fmt.Errorf("while/until loops are not supported")
			return false
		case *syntax.CaseClause:
			err = fmt.Errorf("case statements are not supported")
			return false
		case *syntax.Subshell:
			err = fmt.Errorf("subshells are not supported")
			return false
		case *syntax.FuncDecl:
			err = fmt.Errorf("function declarations are not supported")
			return false
		case *syntax.ArithmCmd:
			err = fmt.Errorf("arithmetic commands are not supported")
			return false
		case *syntax.TestClause:
			err = fmt.Errorf("test expressions are not supported")
			return false
		case *syntax.DeclClause:
			err = fmt.Errorf("%s is not supported", n.Variant.Value)
			return false
		case *syntax.LetClause:
			err = fmt.Errorf("let is not supported")
			return false
		case *syntax.TimeClause:
			err = fmt.Errorf("time is not supported")
			return false
		case *syntax.CoprocClause:
			err = fmt.Errorf("coprocesses are not supported")
			return false
		case *syntax.TestDecl:
			err = fmt.Errorf("test declarations are not supported")
			return false
		case *syntax.ForClause:
			if n.Select {
				err = fmt.Errorf("select statements are not supported")
				return false
			}
			if _, ok := n.Loop.(*syntax.WordIter); !ok {
				err = fmt.Errorf("c-style for loops are not supported")
				return false
			}
		case *syntax.ExtGlob:
			err = fmt.Errorf("extended globbing is not supported")
			return false

		// Blocked statement-level features.
		case *syntax.Stmt:
			if n.Background {
				err = fmt.Errorf("background execution (&) is not supported")
				return false
			}

		// Blocked pipe operators.
		case *syntax.BinaryCmd:
			if n.Op == syntax.PipeAll {
				err = fmt.Errorf("|& is not supported")
				return false
			}

		// Blocked redirections.
		case *syntax.Redirect:
			err = validateRedirect(n)
			if err != nil {
				return false
			}
			if n.Hdoc != nil {
				hdocWords[n.Hdoc] = true
			}

		// Blocked tilde expansion (prevents host user info disclosure via os/user.Lookup).
		// Heredoc bodies are excluded since they don't undergo tilde expansion.
		case *syntax.Word:
			if !hdocWords[n] && len(n.Parts) > 0 {
				if lit, ok := n.Parts[0].(*syntax.Lit); ok {
					if strings.HasPrefix(lit.Value, "~") {
						err = fmt.Errorf("tilde expansion is not supported")
						return false
					}
				}
			}

		// Explicitly allowed command-level nodes. These are the only
		// syntax.Command implementations that safe-shell permits.
		// Listing them here ensures any new Command type added by a future
		// version of mvdan.cc/sh/v3 is caught by the catch-all below.
		// NOTE: *syntax.BinaryCmd and *syntax.ForClause are handled by their
		// own cases above (with partial restrictions) and must not appear here.
		case *syntax.CallExpr, *syntax.IfClause, *syntax.Block:
			// allowed — no action

		// Catch-all for unknown Command types not explicitly listed above.
		// This guards against new command node types added by upstream
		// library upgrades silently bypassing validation.
		case syntax.Command:
			err = fmt.Errorf("unsupported command type: %T", n)
			return false
		}
		return true
	})
	return err
}

// blockedSpecialParams are single-character parameter names that are not
// supported in the safe-shell interpreter (positional params, $#, $0, $@, $*).
var blockedSpecialParams = map[string]bool{
	"#": true,                                  // $# - number of positional parameters
	"!": true,                                  // $! - PID of the last background command
	"0": true,                                  // $0 - name of the shell or script
	"1": true, "2": true, "3": true, "4": true, // $1-$9 - positional parameters
	"5": true, "6": true, "7": true, "8": true, "9": true,
	"@": true, // $@ - all positional parameters as separate words
	"*": true, // $* - all positional parameters as a single word
}

func validateParamExp(pe *syntax.ParamExp) error {
	if pe.Length {
		return fmt.Errorf("${#var} is not supported")
	}
	if pe.Slice != nil {
		return fmt.Errorf("${var:offset} is not supported")
	}
	if pe.Repl != nil {
		return fmt.Errorf("${var/pattern/replacement} is not supported")
	}
	if pe.Excl {
		return fmt.Errorf("${!var} is not supported")
	}
	if pe.Index != nil {
		return fmt.Errorf("array indexing is not supported")
	}
	if pe.Names != 0 {
		return fmt.Errorf("${!prefix*} is not supported")
	}
	if pe.Exp != nil {
		return fmt.Errorf("${var} operations (defaults, pattern removal, case conversion) are not supported")
	}
	// Block special parameters like $#, $0, $1-$9, $@, $*
	if pe.Param != nil && blockedSpecialParams[pe.Param.Value] {
		return fmt.Errorf("$%s is not supported", pe.Param.Value)
	}
	if pe.Param != nil && pe.Param.Value == "LINENO" {
		return fmt.Errorf("$LINENO is not supported")
	}
	return nil
}

func validateAssign(as *syntax.Assign) error {
	if as.Append {
		return fmt.Errorf("+= is not supported")
	}
	if as.Array != nil {
		return fmt.Errorf("array assignment is not supported")
	}
	if as.Index != nil {
		return fmt.Errorf("array index assignment is not supported")
	}
	return nil
}

func validateRedirect(rd *syntax.Redirect) error {
	switch rd.Op {
	case syntax.WordHdoc:
		return fmt.Errorf("<<< (herestring) is not supported")
	case syntax.RdrIn:
		// Input redirection: only fd 0 (stdin) is supported.
		// rd.N is nil when no explicit fd is given (defaults to 0).
		if rd.N != nil && rd.N.Value != "0" {
			return fmt.Errorf("%s< input fd redirection is not supported", rd.N.Value)
		}
		return nil
	case syntax.RdrOut, syntax.ClbOut:
		if redirectTargetIsDevNull(rd) {
			return nil
		}
		return fmt.Errorf("> file redirection is not supported")
	case syntax.AppOut:
		if redirectTargetIsDevNull(rd) {
			return nil
		}
		return fmt.Errorf(">> file redirection is not supported")
	case syntax.RdrAll:
		if redirectTargetIsDevNull(rd) {
			return nil
		}
		return fmt.Errorf("&> file redirection is not supported")
	case syntax.AppAll:
		if redirectTargetIsDevNull(rd) {
			return nil
		}
		return fmt.Errorf("&>> file redirection is not supported")
	case syntax.RdrInOut:
		return fmt.Errorf("<> file redirection is not supported")
	case syntax.DplOut:
		if redirectTargetIsFD(rd) {
			return nil
		}
		return fmt.Errorf(">&N fd duplication is not supported")
	case syntax.DplIn:
		return fmt.Errorf("<&N fd duplication is not supported")
	}
	return nil
}

// redirectTargetIsDevNull reports whether the redirect word is the literal
// path /dev/null (or os.DevNull on Windows). Only simple literal words are
// accepted — variable expansions, globs, and other dynamic forms are rejected
// so that the target cannot be manipulated at runtime. The source fd (rd.N)
// must also be a supported fd (1 or 2).
func redirectTargetIsDevNull(rd *syntax.Redirect) bool {
	// Check source fd: only 1 (stdout) and 2 (stderr) are supported.
	// rd.N is nil when no explicit fd is given (defaults to stdout).
	// For RdrAll/AppAll (&>/&>>), rd.N is always nil since bash does
	// not allow an explicit fd prefix on these ops, so this check is
	// a no-op for them.
	if rd.N != nil && rd.N.Value != "1" && rd.N.Value != "2" {
		return false
	}
	if rd.Word == nil || len(rd.Word.Parts) != 1 {
		return false
	}
	lit, ok := rd.Word.Parts[0].(*syntax.Lit)
	if !ok {
		return false
	}
	return isDevNull(lit.Value)
}

// redirectTargetIsFD reports whether the DplOut (>&N) redirect uses only
// supported file descriptors (1 and 2 for stdout/stderr). Both the source
// fd (rd.N, defaulting to 1) and target fd (rd.Word) must be 1 or 2.
func redirectTargetIsFD(rd *syntax.Redirect) bool {
	// Check source fd (rd.N). If nil, defaults to 1 (stdout), which is fine.
	if rd.N != nil && rd.N.Value != "1" && rd.N.Value != "2" {
		return false
	}
	if rd.Word == nil || len(rd.Word.Parts) != 1 {
		return false
	}
	lit, ok := rd.Word.Parts[0].(*syntax.Lit)
	if !ok {
		return false
	}
	return lit.Value == "1" || lit.Value == "2"
}

// isDevNull reports whether path is the platform's null device.
// On Windows, only the bare "NUL" form is accepted (case-insensitive).
// Device-path prefixes (\\.\NUL, \\?\NUL) and extension variants
// (NUL.txt) are intentionally rejected — exact match keeps the
// allowlist tight.
func isDevNull(path string) bool {
	if path == "/dev/null" {
		return true
	}
	// On Windows, os.DevNull is "NUL". Accept it case-insensitively.
	if os.DevNull != "/dev/null" && strings.EqualFold(path, os.DevNull) {
		return true
	}
	return false
}
