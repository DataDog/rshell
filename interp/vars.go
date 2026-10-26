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
	"sync"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// MaxVarBytes is the maximum size in bytes of a single variable value.
// Assignments that exceed this limit are rejected with an error.
const MaxVarBytes = 1 << 20 // 1 MiB

// MaxTotalVarsBytes is the maximum total size in bytes of all variable values
// a script holds at once. The budget is shared by every subshell and pipeline
// stage of a run that is alive at the same time. Assignments that would push
// the total over this limit are rejected.
const MaxTotalVarsBytes = 1 << 20 // 1 MiB

// MaxTotalVarEntryBytes is the maximum total of variable names plus a fixed
// per-variable overhead (varEntryOverheadBytes) a script holds at once. It
// bounds the number of variables, which MaxTotalVarsBytes alone does not:
// empty values cost nothing against it. With short names this allows roughly
// 16k variables. It is shared like MaxTotalVarsBytes.
const MaxTotalVarEntryBytes = 1 << 20 // 1 MiB

// varEntryOverheadBytes is charged against MaxTotalVarEntryBytes for every
// stored variable in addition to its name. It approximates the map entry and
// expand.Variable bookkeeping, so that empty-valued variables are not free.
const varEntryOverheadBytes = 64

// varStorage is variable storage charged against the run-wide caps: value
// bytes against MaxTotalVarsBytes, and name bytes plus per-variable overhead
// against MaxTotalVarEntryBytes.
type varStorage struct {
	values  int
	entries int
}

func (a varStorage) add(b varStorage) varStorage {
	return varStorage{values: a.values + b.values, entries: a.entries + b.entries}
}

func (a varStorage) sub(b varStorage) varStorage {
	return varStorage{values: a.values - b.values, entries: a.entries - b.entries}
}

// atLeastZero clamps each component at zero.
func (a varStorage) atLeastZero() varStorage {
	return varStorage{values: max(a.values, 0), entries: max(a.entries, 0)}
}

func (a varStorage) grows(from varStorage) bool {
	return a.values > from.values || a.entries > from.entries
}

func (a varStorage) exceedsCaps() bool {
	return a.values > MaxTotalVarsBytes || a.entries > MaxTotalVarEntryBytes
}

// varStorageCost returns what one stored variable is charged. Undeclared
// variables have no entry and cost nothing.
func varStorageCost(name string, vr expand.Variable) varStorage {
	if !vr.Declared() {
		return varStorage{}
	}
	return varStorage{values: len(vr.Str), entries: len(name) + varEntryOverheadBytes}
}

// varStorageBudget is the run-wide variable storage budget shared by every
// overlay derived from the same root environment. live is the sum of what
// each live overlay has added on top of what it inherited (its charged
// storage). Subshells and pipeline stages run on their own overlays, and
// pipeline stages run concurrently, so without a shared budget each one could
// hold the full caps at the same time.
type varStorageBudget struct {
	mu   sync.Mutex
	live varStorage
}

// errTotalVarStorageExceeded is returned by overlayEnviron.Set when the total
// variable storage cap is exceeded. It is a distinct type so that setVar can
// treat it as a fatal error and abort the script.
type errTotalVarStorageExceeded struct {
	total varStorage
}

func (e *errTotalVarStorageExceeded) Error() string {
	if e.total.values > MaxTotalVarsBytes {
		return fmt.Sprintf("variable storage limit exceeded (%d bytes total)", e.total.values)
	}
	return fmt.Sprintf("variable storage limit exceeded (%d bytes of names and entries total)", e.total.entries)
}

func newOverlayEnviron(parent expand.Environ, background bool) *overlayEnviron {
	oenv := &overlayEnviron{}
	if !background {
		oenv.parent = parent
		// Seed total from the parent's tracked counter so that the cap is
		// enforced consistently across subshell nesting levels (preventing a
		// script from resetting the counter to zero at each nesting level and
		// allocating O(depth × MaxTotalVarsBytes) before hitting any limit).
		//
		// When the parent is an *overlayEnviron we use its tracked counter
		// directly, which reflects only script-assigned variables (Env()
		// variables are excluded from the cap via the Reset() zero-out, so they
		// do not appear in pov.total).  Summing via parent.Each() instead
		// would also count inherited Env() variables, producing false cap hits
		// for legitimate callers that provide a large Env() variable.
		//
		// When the parent is not an *overlayEnviron (fallback) we must sum
		// manually since there is no pre-computed counter.
		if pov, ok := parent.(*overlayEnviron); ok {
			oenv.inheritBudget(pov)
		} else {
			parent.Each(func(name string, vr expand.Variable) bool {
				oenv.total = oenv.total.add(varStorageCost(name, vr))
				return true
			})
		}
	} else {
		oenv.values = make(map[string]expand.Variable)
		maps.Insert(oenv.values, parent.Each)
		// Seed total from the parent's tracked counter rather than
		// re-summing all variables (which would include inherited env vars
		// provided via Env() that the main runner intentionally excludes from
		// the cap).  Using the parent's counter keeps background-subshell
		// accounting consistent with the main runner and prevents false positives
		// when the caller provides a large Env() variable: an assignment that
		// succeeds in the parent must also succeed in a pipeline/background subshell.
		if pov, ok := parent.(*overlayEnviron); ok {
			oenv.inheritBudget(pov)
		} else {
			// Fallback for non-overlayEnviron parents: sum all entries.
			for name, vr := range oenv.values {
				oenv.total = oenv.total.add(varStorageCost(name, vr))
			}
		}
	}
	oenv.seed = oenv.total
	return oenv
}

// inheritBudget seeds o's accounting from its parent overlay and joins the
// parent's run-wide budget. The parent's own runner is blocked while a child
// overlay is alive (subshells, command substitutions and pipelines all wait
// for their children), so the ancestors' charged bytes stay constant for the
// child's lifetime and can be recorded once here.
func (o *overlayEnviron) inheritBudget(parent *overlayEnviron) {
	o.total = parent.total
	o.budget = parent.budget
	o.inheritedCharge = parent.inheritedCharge.add(parent.charged)
}

// overlayEnviron is our main implementation of [expand.WriteEnviron].
type overlayEnviron struct {
	// parent is non-nil if [values] is an overlay over a parent environment
	// which we can safely reuse without data races, such as non-background subshells.
	parent expand.Environ
	values map[string]expand.Variable
	// total tracks the script-assigned variable storage counted against
	// MaxTotalVarsBytes and MaxTotalVarEntryBytes (see varStorageCost). For
	// background subshells, where all parent variables are copied into
	// [values], this equals the cost of [values]. For non-background
	// subshells (e.g. ( ) or $( )), it is seeded from the parent's total so
	// parent-inherited variables are included even before any variable is
	// written to [values]; those inherited variables are NOT present in
	// [values] themselves.
	total varStorage

	// budget is the run-wide budget shared with every other overlay derived
	// from the same root; nil for standalone overlays, which then enforce
	// the caps on total alone. seed is total at creation, charged is what
	// this overlay currently holds in budget (total-seed, clamped at zero),
	// and inheritedCharge is the sum of its ancestors' charged storage, which
	// total already includes.
	budget          *varStorageBudget
	seed            varStorage
	charged         varStorage
	inheritedCharge varStorage
}

// account moves o.total to newTotal and updates o's share of the run-wide
// budget. When enforce is set and the total grows, the change is rejected if
// o's own view plus the storage held by every other live overlay (concurrent
// pipeline stages and their subshells) would exceed a cap. The check and the
// update happen under the budget lock so concurrent stages cannot both pass
// against the same snapshot.
func (o *overlayEnviron) account(newTotal varStorage, enforce bool) error {
	// Overriding an uncounted variable (from Env() or the interpreter's own
	// initial assignments) must not create credit.
	newTotal = newTotal.atLeastZero()
	grows := enforce && newTotal.grows(o.total)
	newCharged := newTotal.sub(o.seed).atLeastZero()
	if o.budget == nil {
		if grows && newTotal.exceedsCaps() {
			return &errTotalVarStorageExceeded{total: newTotal}
		}
		o.total = newTotal
		return nil
	}
	o.budget.mu.Lock()
	defer o.budget.mu.Unlock()
	if grows {
		others := o.budget.live.sub(o.inheritedCharge).sub(o.charged)
		if effective := newTotal.add(others); effective.exceedsCaps() {
			return &errTotalVarStorageExceeded{total: effective}
		}
	}
	o.budget.live = o.budget.live.add(newCharged).sub(o.charged)
	o.total = newTotal
	o.charged = newCharged
	return nil
}

// release returns o's charged storage to the run-wide budget. It must be
// called once the subshell or pipeline stage using o has finished; o must not
// be written afterwards.
func (o *overlayEnviron) release() {
	if o.budget != nil {
		o.budget.mu.Lock()
		o.budget.live = o.budget.live.sub(o.charged)
		o.budget.mu.Unlock()
	}
	o.charged = varStorage{}
}

// resetAccounting zeroes o's accounting and starts a fresh run-wide budget
// rooted at o.
func (o *overlayEnviron) resetAccounting() {
	o.budget = &varStorageBudget{}
	o.total = varStorage{}
	o.seed = varStorage{}
	o.charged = varStorage{}
	o.inheritedCharge = varStorage{}
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
		// Credit prev's cost unconditionally: non-background subshells seed
		// total from the parent, so parent-inherited vars are already
		// counted even when !inOverlay.
		if prev.Local {
			// The unset local retains its slot but no value.
			vr.Local = true
			_ = o.account(o.total.sub(varStorageCost(name, prev)).add(varStorageCost(name, vr)), false)
			o.values[name] = vr
			return nil
		}
		_ = o.account(o.total.sub(varStorageCost(name, prev)), false)
		delete(o.values, name)
		return nil
	}
	// modifying the entire variable — enforce total storage cap.
	// Credit the previous entry's cost unconditionally: for non-background
	// subshells, newOverlayEnviron seeds total from the parent, so the
	// parent's entry is already counted. If we only credited it when
	// inOverlay (i.e. the variable was already in our overlay), we would
	// double-charge the parent's contribution on the first override.
	vr.Local = prev.Local || vr.Local
	if err := o.account(o.total.sub(varStorageCost(name, prev)).add(varStorageCost(name, vr)), true); err != nil {
		return err
	}
	o.values[name] = vr
	return nil
}

// setUncapped sets a variable without enforcing MaxTotalVarsBytes.  It is
// used by setVarRestore to restore inline command variables (e.g. FOO=val cmd)
// to their original values after the command returns, even when the script
// has filled storage close to the cap during command execution.  total
// is still updated so that subsequent cap checks use an accurate baseline.
func (o *overlayEnviron) setUncapped(name string, vr expand.Variable) {
	if o.values == nil {
		o.values = make(map[string]expand.Variable)
	}
	prev, inOverlay := o.values[name]
	if !inOverlay && o.parent != nil {
		prev = o.parent.Get(name)
	}
	vr.Local = prev.Local || vr.Local
	_ = o.account(o.total.sub(varStorageCost(name, prev)).add(varStorageCost(name, vr)), false)
	o.values[name] = vr
}

func (o *overlayEnviron) Each(f func(name string, vr expand.Variable) bool) {
	// Emit our own overrides first so they take precedence.
	for name, vr := range o.values {
		if !f(name, vr) {
			return
		}
	}
	// Then emit parent variables, skipping any that we already overrode above.
	// Without this guard, a variable that exists in both the parent environment
	// and our overlay (because the script re-assigned it) would be emitted twice,
	// causing newOverlayEnviron to seed total at 2× the real storage.
	if o.parent != nil {
		o.parent.Each(func(name string, vr expand.Variable) bool {
			if _, override := o.values[name]; override {
				return true // already emitted from our overlay
			}
			return f(name, vr)
		})
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

// setVarRestore writes a variable back without enforcing any size limits.
// Used to restore inline command variables (e.g. FOO=val cmd) to their
// original values after the command returns, so that inherited variables
// larger than MaxVarBytes or near the total cap can be restored correctly.
// If the underlying env is an overlayEnviron, it bypasses the total-bytes
// cap to avoid erroneously blocking the restore when the script filled storage
// during the command's execution.
func (r *Runner) setVarRestore(name string, vr expand.Variable) {
	if ov, ok := r.writeEnv.(*overlayEnviron); ok {
		ov.setUncapped(name, vr)
		return
	}
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
		prev.Str = r.assignmentLiteral(as.Name.Value, as.Value)
		return prev
	}
	// Bare assignment (e.g. VAR=)
	prev.Kind = expand.String
	prev.Str = ""
	return prev
}
