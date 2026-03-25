// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"errors"
	"fmt"
	"maps"
	"runtime"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// MaxVarBytes is the maximum size in bytes of a single variable value.
// Assignments that exceed this limit are rejected with an error.
const MaxVarBytes = 1 << 20 // 1 MiB

// MaxTotalVarsBytes is the maximum total size in bytes of all variable values
// combined. Assignments that would push the total over this limit are rejected.
const MaxTotalVarsBytes = 1 << 20 // 1 MiB

// errTotalVarStorageExceeded is returned by overlayEnviron.Set when the total
// variable storage cap is exceeded. It is a distinct type so that setVar can
// treat it as a fatal error and abort the script.
type errTotalVarStorageExceeded struct {
	total int
}

func (e *errTotalVarStorageExceeded) Error() string {
	return fmt.Sprintf("variable storage limit exceeded (%d bytes total)", e.total)
}

func newOverlayEnviron(parent expand.Environ, background bool) *overlayEnviron {
	oenv := &overlayEnviron{}
	if !background {
		oenv.parent = parent
	} else {
		// We could do better here if the parent is also an overlayEnviron;
		// measure with profiles or benchmarks before we choose to do so.
		oenv.values = make(map[string]expand.Variable)
		maps.Insert(oenv.values, parent.Each)
		for _, vr := range oenv.values {
			oenv.totalBytes += len(vr.Str)
		}
	}
	return oenv
}

// overlayEnviron is our main implementation of [expand.WriteEnviron].
type overlayEnviron struct {
	// parent is non-nil if [values] is an overlay over a parent environment
	// which we can safely reuse without data races, such as non-background subshells.
	parent expand.Environ
	values map[string]expand.Variable
	// totalBytes tracks the sum of len(value.Str) for all variables in [values].
	totalBytes int
}

func (o *overlayEnviron) Get(name string) expand.Variable {
	if vr, ok := o.values[name]; ok {
		return vr
	}
	if o.parent != nil {
		return o.parent.Get(name)
	}
	return expand.Variable{}
}

func (o *overlayEnviron) Set(name string, vr expand.Variable) error {
	prev, inOverlay := o.values[name]
	if !inOverlay && o.parent != nil {
		prev = o.parent.Get(name)
	}

	if o.values == nil {
		o.values = make(map[string]expand.Variable)
	}
	if prev.ReadOnly && vr.Kind != expand.KeepValue {
		return fmt.Errorf("readonly variable")
	}
	if vr.Kind == expand.KeepValue {
		if prev.ReadOnly {
			return fmt.Errorf("readonly variable")
		}
		vr.Kind = prev.Kind
		vr.Str = prev.Str
		vr.List = prev.List
		vr.Map = prev.Map
	}
	if !vr.IsSet() { // unsetting
		// Note: prev.ReadOnly is always false here (guarded by the checks above),
		// but we keep this as defense-in-depth in case future refactors change the flow.
		if prev.ReadOnly {
			return fmt.Errorf("readonly variable")
		}
		if prev.Local {
			vr.Local = true
			// Subtract old value from total; the unset local retains its slot but no value.
			if inOverlay {
				o.totalBytes -= len(prev.Str)
			}
			o.values[name] = vr
			return nil
		}
		if inOverlay {
			o.totalBytes -= len(prev.Str)
		}
		delete(o.values, name)
		return nil
	}
	// modifying the entire variable — enforce total storage cap
	oldBytes := 0
	if inOverlay {
		oldBytes = len(prev.Str)
	}
	newBytes := len(vr.Str)
	delta := newBytes - oldBytes
	if delta > 0 && o.totalBytes+delta > MaxTotalVarsBytes {
		return &errTotalVarStorageExceeded{total: o.totalBytes + delta}
	}
	o.totalBytes += delta
	vr.Local = prev.Local || vr.Local
	o.values[name] = vr
	return nil
}

func (o *overlayEnviron) Each(f func(name string, vr expand.Variable) bool) {
	if o.parent != nil {
		o.parent.Each(f)
	}
	for name, vr := range o.values {
		if !f(name, vr) {
			return
		}
	}
}

// internalErrorf records an internal assertion failure as a fatal error.
// Use this for conditions that should be unreachable (e.g. invariants
// enforced by AST validation).
func (r *Runner) internalErrorf(format string, a ...any) {
	r.exit.fatal(fmt.Errorf("internal error: "+format, a...))
}

func (r *Runner) lookupVar(name string) expand.Variable {
	if name == "" {
		r.internalErrorf("variable name must not be empty")
		return expand.Variable{}
	}
	// Only $? is supported as a special variable in safe-shell.
	if name == "?" {
		return expand.Variable{
			Set:  true,
			Kind: expand.String,
			Str:  strconv.Itoa(int(r.lastExit.code)),
		}
	}
	if vr := r.writeEnv.Get(name); vr.Declared() {
		return vr
	}
	if runtime.GOOS == "windows" {
		upper := strings.ToUpper(name)
		if vr := r.writeEnv.Get(upper); vr.Declared() {
			return vr
		}
	}
	return expand.Variable{}
}

func (r *Runner) setVarString(name, value string) {
	r.setVar(name, expand.Variable{Set: true, Kind: expand.String, Str: value})
}

// setVarErr is like setVar but returns the error instead of recording it as a
// side-effect. Use this when the caller needs to propagate the error (e.g. in
// the expand package's WriteEnviron.Set callback).
func (r *Runner) setVarErr(name string, vr expand.Variable) error {
	return r.writeEnv.Set(name, vr)
}

func (r *Runner) setVar(name string, vr expand.Variable) {
	if vr.IsSet() && len(vr.Str) > MaxVarBytes {
		r.errf("%s: value too large (limit %d bytes)\n", name, MaxVarBytes)
		r.exit.code = 1
		return
	}
	if err := r.writeEnv.Set(name, vr); err != nil {
		r.errf("%s: %v\n", name, err)
		var storageErr *errTotalVarStorageExceeded
		if errors.As(err, &storageErr) {
			// Total storage exhaustion aborts the script (sets exiting without
			// recording a fatal error, so Run returns ExitStatus(1) rather than
			// the raw error, which is consistent with how the test helpers work).
			r.exit.code = 1
			r.exit.exiting = true
		} else {
			r.exit.code = 1
		}
		return
	}
}

// setVarRestore writes a variable back without enforcing the size limit.
// Used to restore inline command variables (e.g. FOO=val cmd) to their
// original values after the command returns, so that inherited variables
// larger than MaxVarBytes can be restored correctly.
func (r *Runner) setVarRestore(name string, vr expand.Variable) {
	if err := r.writeEnv.Set(name, vr); err != nil {
		r.errf("%s: %v\n", name, err)
		r.exit.code = 1
	}
}

// setVarWithIndex sets a variable.  In safe-shell, arrays and indexing are
// blocked by the AST validator, so we only handle simple string assignment.
func (r *Runner) setVarWithIndex(prev expand.Variable, name string, index syntax.ArithmExpr, vr expand.Variable) {
	if index != nil {
		r.internalErrorf("setVarWithIndex: index should have been rejected by AST validation")
		return
	}
	prev.Set = true
	if name2, var2 := prev.Resolve(r.writeEnv); name2 != "" {
		name = name2
		prev = var2
	}
	r.setVar(name, vr)
}

// assignVal evaluates the value of an assignment.  In safe-shell, only simple
// string assignments are supported (no append, no arrays, no NameRef).  The AST
// validator rejects those constructs before we get here, so hitting them is a
// programming error.
func (r *Runner) assignVal(prev expand.Variable, as *syntax.Assign, _ string) expand.Variable {
	prev.Set = true
	if as.Append {
		r.internalErrorf("assignVal: append should have been rejected by AST validation")
		return expand.Variable{}
	}
	if as.Array != nil {
		r.internalErrorf("assignVal: array assignment should have been rejected by AST validation")
		return expand.Variable{}
	}
	if as.Value != nil {
		prev.Kind = expand.String
		prev.Str = r.literal(as.Value)
		return prev
	}
	// Bare assignment (e.g. VAR=)
	prev.Kind = expand.String
	prev.Str = ""
	return prev
}
