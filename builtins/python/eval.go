// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package python

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/big"
	"strings"
)

// maxCallDepth is the maximum recursion depth for function calls.
const maxCallDepth = 500

// maxRepeatBytes is the maximum number of bytes that may be produced by a
// sequence-repetition operation (str * n, bytes * n, list * n, tuple * n).
// Exceeding this limit raises MemoryError, preventing OOM attacks via large n.
const maxRepeatBytes = 1 << 20 // 1 MiB

// genChannels holds the channels used inside a generator goroutine.
type genChannels struct {
	sendCh  chan Object
	yieldCh chan Object
	ctx     context.Context
}

// Evaluator is the tree-walking evaluator.
type Evaluator struct {
	ctx             context.Context
	scope           *Scope
	globals         map[string]Object
	opts            *RunOpts
	modules         map[string]*PyModule
	genState        *genChannels
	depth           int
	activeException *PyException
}

// newEvaluator creates an Evaluator rooted at the module scope and registers its
// callObject for the current goroutine. The returned cleanup function must be
// deferred by the caller to deregister the entry when execution finishes.
func newEvaluator(ctx context.Context, opts *RunOpts, globals map[string]Object, modules map[string]*PyModule) (*Evaluator, func()) {
	scope := newModuleScope(globals)
	e := &Evaluator{
		ctx:     ctx,
		scope:   scope,
		globals: globals,
		opts:    opts,
		modules: modules,
	}
	// Register the evaluator's callObject for this goroutine so that types.go
	// and builtins_funcs.go can call user-defined functions without a shared global.
	gid, ok := goroutineID()
	if !ok {
		// Parsing failed — degrade gracefully rather than crashing the shell.
		// callObject will raise RuntimeError if invoked in this state.
		return e, func() {}
	}
	goroutineCallFns.Store(gid, func(fn Object, args []Object, kwargs map[string]Object) Object {
		return e.callObject(fn, args, kwargs)
	})
	return e, func() { goroutineCallFns.Delete(gid) }
}

// checkCtx panics with KeyboardInterrupt if the context has been cancelled.
func (e *Evaluator) checkCtx() {
	select {
	case <-e.ctx.Done():
		panic(exceptionSignal{exc: newExceptionf(ExcKeyboardInterrupt, "interrupted")})
	default:
	}
}

// ---- Statement execution ----

// exec dispatches each statement in the list.
func (e *Evaluator) exec(stmts []Stmt) {
	for _, s := range stmts {
		e.execStmt(s)
	}
}

func (e *Evaluator) execStmt(s Stmt) {
	switch n := s.(type) {
	case *AssignStmt:
		e.execAssign(n)
	case *AugAssignStmt:
		e.execAugAssign(n)
	case *AnnAssignStmt:
		e.execAnnAssign(n)
	case *ExprStmt:
		e.eval(n.Value)
	case *IfStmt:
		e.execIf(n)
	case *WhileStmt:
		e.execWhile(n)
	case *ForStmt:
		e.execFor(n)
	case *FuncDef:
		e.execFuncDef(n)
	case *ClassDef:
		e.execClassDef(n)
	case *ReturnStmt:
		e.execReturn(n)
	case *BreakStmt:
		panic(controlSignal{kind: ctrlBreak})
	case *ContinueStmt:
		panic(controlSignal{kind: ctrlContinue})
	case *PassStmt:
		// nothing
	case *RaiseStmt:
		e.execRaise(n)
	case *TryStmt:
		e.execTry(n)
	case *WithStmt:
		e.execWith(n)
	case *ImportStmt:
		e.execImport(n)
	case *ImportFromStmt:
		e.execImportFrom(n)
	case *GlobalStmt:
		e.execGlobal(n)
	case *NonlocalStmt:
		e.execNonlocal(n)
	case *DelStmt:
		e.execDel(n)
	case *AssertStmt:
		e.execAssert(n)
	}
}

func (e *Evaluator) execAssign(n *AssignStmt) {
	val := e.eval(n.Value)
	for _, target := range n.Targets {
		e.assign(target, val)
	}
}

func (e *Evaluator) execAugAssign(n *AugAssignStmt) {
	current := e.eval(n.Target)
	rhs := e.eval(n.Value)
	// Strip trailing '=' from augmented operator (e.g. "+=" → "+").
	op := strings.TrimSuffix(n.Op, "=")
	result := e.applyBinOp(op, current, rhs)
	e.assign(n.Target, result)
}

func (e *Evaluator) execAnnAssign(n *AnnAssignStmt) {
	if n.Value != nil {
		val := e.eval(n.Value)
		e.assign(n.Target, val)
	}
}

func (e *Evaluator) execIf(n *IfStmt) {
	if pyTruth(e.eval(n.Test)) {
		e.exec(n.Body)
	} else {
		e.exec(n.Orelse)
	}
}

func (e *Evaluator) execWhile(n *WhileStmt) {
	for {
		e.checkCtx()
		if !pyTruth(e.eval(n.Test)) {
			break
		}
		brk := e.execLoopBody(n.Body)
		if brk {
			return
		}
	}
	e.exec(n.Orelse)
}

func (e *Evaluator) execFor(n *ForStmt) {
	items := e.iterateObj(e.eval(n.Iter))
	for _, item := range items {
		e.checkCtx()
		e.assign(n.Target, item)
		brk := e.execLoopBody(n.Body)
		if brk {
			return
		}
	}
	e.exec(n.Orelse)
}

// execLoopBody runs the body, returning true if a break was hit.
func (e *Evaluator) execLoopBody(body []Stmt) (brk bool) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if sig, ok := r.(controlSignal); ok {
			switch sig.kind {
			case ctrlBreak:
				brk = true
				return
			case ctrlContinue:
				return
			}
		}
		panic(r)
	}()
	e.exec(body)
	return false
}

func (e *Evaluator) execFuncDef(n *FuncDef) {
	// Evaluate defaults in current scope
	defaults := make([]Object, len(n.Args.Defaults))
	for i, d := range n.Args.Defaults {
		defaults[i] = e.eval(d)
	}
	kwDefaults := make(map[string]Object)
	for i, kw := range n.Args.KwOnly {
		if i < len(n.Args.KwDefaults) && n.Args.KwDefaults[i] != nil {
			kwDefaults[kw] = e.eval(n.Args.KwDefaults[i])
		}
	}

	fn := &PyFunction{
		Name:       n.Name,
		Args:       n.Args,
		Body:       n.Body,
		Closure:    e.scope,
		Globals:    e.globals,
		Defaults:   defaults,
		KwDefaults: kwDefaults,
		IsGen:      n.IsGen,
	}

	// Apply decorators in reverse order
	var obj Object = fn
	for i := len(n.Decorators) - 1; i >= 0; i-- {
		dec := e.eval(n.Decorators[i])
		obj = e.callObject(dec, []Object{obj}, nil)
	}

	e.scope.set(n.Name, obj)
}

// objectClass is the implicit base class for all user-defined classes.
var objectClass = &PyClass{Name: "object", Dict: make(map[string]Object)}

func init() {
	objectClass.MRO = []*PyClass{objectClass}
}

func (e *Evaluator) execClassDef(n *ClassDef) {
	// Resolve base classes before executing body
	bases := make([]*PyClass, 0, len(n.Bases))
	for _, b := range n.Bases {
		bObj := e.eval(b)
		switch bc := bObj.(type) {
		case *PyClass:
			bases = append(bases, bc)
		default:
			raiseTypeError("bases must be classes, not %s", bObj.pyType().Name)
		}
	}
	if len(bases) == 0 {
		bases = []*PyClass{objectClass}
	}

	// Execute class body in a new scope
	classScope := newFunctionScope(e.scope, e.globals, n.Name)
	classScope.class = &PyClass{Name: n.Name} // placeholder for __class__ ref

	child := &Evaluator{
		ctx:     e.ctx,
		scope:   classScope,
		globals: e.globals,
		opts:    e.opts,
		modules: e.modules,
		depth:   e.depth,
	}
	// Propagate callObject binding
	child.exec(n.Body)

	// Collect class dict
	classDict := make(map[string]Object, len(classScope.vars))
	for k, v := range classScope.vars {
		classDict[k] = v
	}

	cls := &PyClass{Name: n.Name, Bases: bases, Dict: classDict}
	cls.MRO = computeMRO(cls)

	// Bind __class__ in methods so super() works
	classScope.class = cls

	// Apply decorators
	var obj Object = cls
	for i := len(n.Decorators) - 1; i >= 0; i-- {
		dec := e.eval(n.Decorators[i])
		obj = e.callObject(dec, []Object{obj}, nil)
	}

	e.scope.set(n.Name, obj)
}

func (e *Evaluator) execReturn(n *ReturnStmt) {
	var val Object = pyNone
	if n.Value != nil {
		val = e.eval(n.Value)
	}
	panic(controlSignal{kind: ctrlReturn, value: val})
}

func (e *Evaluator) execRaise(n *RaiseStmt) {
	if n.Exc == nil {
		// bare raise — re-raise active exception
		if e.activeException != nil {
			exc := e.activeException
			if n.Cause != nil {
				cause := e.eval(n.Cause)
				if ce, ok := cause.(*PyException); ok {
					exc.Cause = ce
				}
			}
			panic(exceptionSignal{exc: exc})
		}
		panic(exceptionSignal{exc: newExceptionf(ExcRuntimeError, "No active exception to re-raise")})
	}

	excVal := e.eval(n.Exc)
	var exc *PyException
	switch v := excVal.(type) {
	case *PyException:
		exc = v
	case *PyClass:
		// Bare class raise: raise ValueError → instantiate with no args
		exc = newException(v)
	default:
		raiseTypeError("exceptions must derive from BaseException")
	}

	if n.Cause != nil {
		causeVal := e.eval(n.Cause)
		switch cv := causeVal.(type) {
		case *PyException:
			exc.Cause = cv
		case *PyClass:
			exc.Cause = newException(cv)
		}
	}

	panic(exceptionSignal{exc: exc})
}

func (e *Evaluator) execTry(n *TryStmt) {
	var handlerPanic interface{}

	// Outer defer runs finally block
	defer func() {
		if len(n.Finally) > 0 {
			r := recover()
			e.exec(n.Finally)
			if r != nil {
				panic(r)
			}
		}
	}()

	// Inner function handles except clauses
	func() {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			sig, ok := r.(exceptionSignal)
			if !ok {
				handlerPanic = r
				return
			}

			// Try each except handler
			for _, h := range n.Handlers {
				if e.handlerMatches(sig.exc, h) {
					prevExc := e.activeException
					e.activeException = sig.exc
					if h.Name != "" {
						e.scope.set(h.Name, sig.exc)
					}
					defer func() {
						e.activeException = prevExc
						if h.Name != "" {
							e.scope.delete(h.Name)
						}
					}()
					e.exec(h.Body)
					return
				}
			}
			// No handler matched — re-panic
			handlerPanic = sig
		}()
		e.exec(n.Body)
		// else clause runs only if no exception
		e.exec(n.Orelse)
	}()

	if handlerPanic != nil {
		panic(handlerPanic)
	}
}

func (e *Evaluator) handlerMatches(exc *PyException, h *ExceptHandler) bool {
	if h.Type == nil {
		return true // bare except
	}
	typeVal := e.eval(h.Type)
	switch tv := typeVal.(type) {
	case *PyClass:
		return exceptionMatchesClass(exc, tv)
	case *PyTuple:
		for _, item := range tv.items {
			if cls, ok := item.(*PyClass); ok {
				if exceptionMatchesClass(exc, cls) {
					return true
				}
			}
		}
	}
	return false
}

func (e *Evaluator) execWith(n *WithStmt) {
	type ctxEntry struct {
		mgr     Object
		optVar  Expr
		entered Object
	}

	entries := make([]ctxEntry, 0, len(n.Items))
	for _, item := range n.Items {
		mgr := e.eval(item.CtxExpr)
		entered := e.callMethod(mgr, "__enter__", nil, nil)
		entries = append(entries, ctxEntry{mgr: mgr, optVar: item.OptVar, entered: entered})
		if item.OptVar != nil {
			e.assign(item.OptVar, entered)
		}
	}

	var bodyPanic interface{}
	func() {
		defer func() {
			bodyPanic = recover()
		}()
		e.exec(n.Body)
	}()

	// Call __exit__ for each context manager in reverse order
	suppress := false
	for i := len(entries) - 1; i >= 0; i-- {
		mgr := entries[i].mgr
		var result Object
		if bodyPanic != nil {
			if sig, ok := bodyPanic.(exceptionSignal); ok {
				result = e.callMethod(mgr, "__exit__", []Object{sig.exc, sig.exc, pyNone}, nil)
			} else {
				result = e.callMethod(mgr, "__exit__", []Object{pyNone, pyNone, pyNone}, nil)
			}
		} else {
			result = e.callMethod(mgr, "__exit__", []Object{pyNone, pyNone, pyNone}, nil)
		}
		if pyTruth(result) {
			suppress = true
		}
	}

	if bodyPanic != nil && !suppress {
		panic(bodyPanic)
	}
}

func (e *Evaluator) execImport(n *ImportStmt) {
	for _, name := range n.Names {
		mod, found := loadModule(name.Name, e.opts)
		if !found {
			// Check cache
			if cached, ok := e.modules[name.Name]; ok {
				mod = cached
			} else {
				panic(exceptionSignal{exc: newExceptionf(ExcImportError, "No module named '%s'", name.Name)})
			}
		}
		if mod != nil {
			e.modules[name.Name] = mod
		}

		bindName := name.Name
		if name.Alias != "" {
			bindName = name.Alias
		} else {
			// For "import a.b", bind the top-level name
			dotIdx := 0
			for dotIdx < len(bindName) && bindName[dotIdx] != '.' {
				dotIdx++
			}
			bindName = bindName[:dotIdx]
		}
		if mod != nil {
			e.scope.set(bindName, mod)
		}
	}
}

func (e *Evaluator) execImportFrom(n *ImportFromStmt) {
	mod, found := loadModule(n.Module, e.opts)
	if !found {
		if cached, ok := e.modules[n.Module]; ok {
			mod = cached
		} else {
			panic(exceptionSignal{exc: newExceptionf(ExcImportError, "No module named '%s'", n.Module)})
		}
	}
	if mod != nil {
		e.modules[n.Module] = mod
	}

	if len(n.Names) == 1 && n.Names[0].Name == "*" {
		// Star import
		if mod != nil {
			for k, v := range mod.Dict {
				e.scope.set(k, v)
			}
		}
		return
	}

	for _, name := range n.Names {
		if mod == nil {
			panic(exceptionSignal{exc: newExceptionf(ExcImportError, "cannot import name '%s' from '%s'", name.Name, n.Module)})
		}
		val, ok := mod.Dict[name.Name]
		if !ok {
			panic(exceptionSignal{exc: newExceptionf(ExcImportError, "cannot import name '%s' from '%s'", name.Name, n.Module)})
		}
		bindName := name.Name
		if name.Alias != "" {
			bindName = name.Alias
		}
		e.scope.set(bindName, val)
	}
}

func (e *Evaluator) execGlobal(n *GlobalStmt) {
	if e.scope.globalNames == nil {
		e.scope.globalNames = make(map[string]bool)
	}
	for _, name := range n.Names {
		e.scope.globalNames[name] = true
	}
}

func (e *Evaluator) execNonlocal(n *NonlocalStmt) {
	if e.scope.nonlocalNames == nil {
		e.scope.nonlocalNames = make(map[string]bool)
	}
	for _, name := range n.Names {
		e.scope.nonlocalNames[name] = true
	}
}

func (e *Evaluator) execDel(n *DelStmt) {
	for _, target := range n.Targets {
		e.delTarget(target)
	}
}

func (e *Evaluator) delTarget(target Expr) {
	switch t := target.(type) {
	case *NameExpr:
		if !e.scope.delete(t.Id) {
			// Check globals
			if _, ok := e.globals[t.Id]; ok {
				delete(e.globals, t.Id)
			} else {
				raiseNameError(t.Id)
			}
		}
	case *AttributeExpr:
		obj := e.eval(t.Value)
		switch v := obj.(type) {
		case *PyInstance:
			delete(v.Dict, t.Attr)
		case *PyClass:
			delete(v.Dict, t.Attr)
		default:
			raiseAttributeError(obj.pyType().Name, t.Attr)
		}
	case *SubscriptExpr:
		obj := e.eval(t.Value)
		key := e.eval(t.Slice)
		e.delItem(obj, key)
	case *TupleExpr:
		for _, elt := range t.Elts {
			e.delTarget(elt)
		}
	case *ListExpr:
		for _, elt := range t.Elts {
			e.delTarget(elt)
		}
	}
}

func (e *Evaluator) delItem(obj Object, key Object) {
	switch v := obj.(type) {
	case *PyDict:
		if !v.del(key) {
			raiseKeyError(key)
		}
	case *PyList:
		idx := int(toIntVal(key))
		if idx < 0 {
			idx = len(v.items) + idx
		}
		if idx < 0 || idx >= len(v.items) {
			raiseIndexError("list assignment index out of range")
		}
		v.items = append(v.items[:idx], v.items[idx+1:]...)
	default:
		// Try __delitem__
		if inst, ok := obj.(*PyInstance); ok {
			if fn, ok2 := inst.lookupMethod("__delitem__"); ok2 {
				e.callObject(fn, []Object{inst, key}, nil)
				return
			}
		}
		raiseTypeError("'%s' object doesn't support item deletion", obj.pyType().Name)
	}
}

func (e *Evaluator) execAssert(n *AssertStmt) {
	if !pyTruth(e.eval(n.Test)) {
		var msg Object = pyNone
		if n.Msg != nil {
			msg = e.eval(n.Msg)
		}
		if msg == pyNone {
			panic(exceptionSignal{exc: newException(ExcAssertionError)})
		}
		panic(exceptionSignal{exc: newExceptionf(ExcAssertionError, "%s", msg.pyStr())})
	}
}

// ---- Expression evaluation ----

// eval evaluates an expression node and returns the result.
func (e *Evaluator) eval(node Node) Object {
	if node == nil {
		return pyNone
	}
	switch n := node.(type) {
	case *BinOp:
		return e.evalBinOp(n)
	case *UnaryOp:
		return e.evalUnaryOp(n)
	case *BoolOp:
		return e.evalBoolOp(n)
	case *Compare:
		return e.evalCompare(n)
	case *CallExpr:
		return e.evalCall(n)
	case *AttributeExpr:
		return e.evalAttribute(n)
	case *SubscriptExpr:
		return e.evalSubscript(n)
	case *SliceExpr:
		return e.evalSlice(n)
	case *NameExpr:
		return e.evalName(n)
	case *Constant:
		return e.evalConstant(n)
	case *ListExpr:
		return e.evalList(n)
	case *TupleExpr:
		return e.evalTuple(n)
	case *DictExpr:
		return e.evalDict(n)
	case *SetExpr:
		return e.evalSet(n)
	case *IfExp:
		return e.evalIfExp(n)
	case *Lambda:
		return e.evalLambda(n)
	case *ListComp:
		return e.evalListComp(n)
	case *DictComp:
		return e.evalDictComp(n)
	case *SetComp:
		return e.evalSetComp(n)
	case *GeneratorExp:
		return e.evalGeneratorExp(n)
	case *Yield:
		return e.evalYield(n)
	case *YieldFrom:
		return e.evalYieldFrom(n)
	case *Starred:
		// Starred outside of assignment context: evaluate inner value
		return e.eval(n.Value)
	}
	return pyNone
}

func (e *Evaluator) evalBinOp(n *BinOp) Object {
	left := e.eval(n.Left)
	// Short-circuit-safe: right is evaluated after left
	right := e.eval(n.Right)
	return e.applyBinOp(n.Op, left, right)
}

func (e *Evaluator) applyBinOp(op string, left, right Object) Object {
	switch op {
	case "+":
		return pyAdd(left, right)
	case "-":
		// set - set = difference
		if ls, ok := left.(*PySet); ok {
			if rs, ok2 := right.(*PySet); ok2 {
				result := &PySet{items: make(map[any]Object)}
				for k, item := range ls.items {
					if _, ok3 := rs.items[k]; !ok3 {
						result.items[k] = item
					}
				}
				return result
			}
		}
		return e.numericBinOp(op, left, right)
	case "*":
		return e.mulOp(left, right)
	case "/":
		return e.divOp(left, right)
	case "//":
		return e.floorDivOp(left, right)
	case "%":
		return e.modOp(left, right)
	case "**":
		return e.powOp(left, right)
	case "&":
		return e.bitwiseOp(op, left, right)
	case "|":
		return e.bitwiseOrOp(left, right)
	case "^":
		return e.bitwiseOp(op, left, right)
	case "<<":
		return e.bitwiseOp(op, left, right)
	case ">>":
		return e.bitwiseOp(op, left, right)
	case "@":
		// matmul: try __matmul__
		if inst, ok := left.(*PyInstance); ok {
			if fn, ok2 := inst.lookupMethod("__matmul__"); ok2 {
				return e.callObject(fn, []Object{inst, right}, nil)
			}
		}
		raiseTypeError("unsupported operand type(s) for @: '%s' and '%s'", left.pyType().Name, right.pyType().Name)
	}
	raiseTypeError("unsupported operator: %s", op)
	return nil
}

func (e *Evaluator) numericBinOp(op string, left, right Object) Object {
	// Normalize bools to int
	left = normBool(left)
	right = normBool(right)

	switch lv := left.(type) {
	case *PyInt:
		switch rv := right.(type) {
		case *PyInt:
			la, ra := lv.toBigInt(), rv.toBigInt()
			var result *big.Int
			switch op {
			case "-":
				result = new(big.Int).Sub(la, ra)
			default:
				raiseTypeError("unsupported int op %s", op)
			}
			return pyIntBig(result)
		case *PyFloat:
			if n, ok := lv.int64(); ok {
				switch op {
				case "-":
					return pyFloat(float64(n) - rv.v)
				}
			}
		}
	case *PyFloat:
		rf := toFloatVal(right)
		switch op {
		case "-":
			return pyFloat(lv.v - rf)
		}
	}
	raiseTypeError("unsupported operand type(s) for %s: '%s' and '%s'", op, left.pyType().Name, right.pyType().Name)
	return nil
}

func (e *Evaluator) mulOp(left, right Object) Object {
	// str * int, int * str, list * int, int * list
	switch lv := left.(type) {
	case *PyStr:
		if n, ok := toOptInt(right); ok {
			if n <= 0 {
				return pyStr("")
			}
			checkRepeatBytesLimit(len(lv.v), n)
			result := make([]byte, 0, len(lv.v)*int(n))
			for i := int64(0); i < n; i++ {
				result = append(result, lv.v...)
			}
			return pyStr(string(result))
		}
	case *PyList:
		if n, ok := toOptInt(right); ok {
			if n <= 0 {
				return pyList(nil)
			}
			checkRepeatItemsLimit(len(lv.items), n)
			items := make([]Object, 0, len(lv.items)*int(n))
			for i := int64(0); i < n; i++ {
				items = append(items, lv.items...)
			}
			return pyList(items)
		}
	case *PyTuple:
		if n, ok := toOptInt(right); ok {
			if n <= 0 {
				return pyTuple(nil)
			}
			checkRepeatItemsLimit(len(lv.items), n)
			items := make([]Object, 0, len(lv.items)*int(n))
			for i := int64(0); i < n; i++ {
				items = append(items, lv.items...)
			}
			return pyTuple(items)
		}
	case *PyBytes:
		if n, ok := toOptInt(right); ok {
			if n <= 0 {
				return pyBytes(nil)
			}
			checkRepeatBytesLimit(len(lv.v), n)
			result := make([]byte, 0, len(lv.v)*int(n))
			for i := int64(0); i < n; i++ {
				result = append(result, lv.v...)
			}
			return pyBytes(result)
		}
	case *PyInt, *PyBool:
		// int * str, int * list, etc.
		n := toIntVal(left)
		switch rv := right.(type) {
		case *PyStr:
			if n <= 0 {
				return pyStr("")
			}
			checkRepeatBytesLimit(len(rv.v), n)
			result := make([]byte, 0, len(rv.v)*int(n))
			for i := int64(0); i < n; i++ {
				result = append(result, rv.v...)
			}
			return pyStr(string(result))
		case *PyList:
			if n <= 0 {
				return pyList(nil)
			}
			checkRepeatItemsLimit(len(rv.items), n)
			items := make([]Object, 0, len(rv.items)*int(n))
			for i := int64(0); i < n; i++ {
				items = append(items, rv.items...)
			}
			return pyList(items)
		case *PyTuple:
			if n <= 0 {
				return pyTuple(nil)
			}
			checkRepeatItemsLimit(len(rv.items), n)
			items := make([]Object, 0, len(rv.items)*int(n))
			for i := int64(0); i < n; i++ {
				items = append(items, rv.items...)
			}
			return pyTuple(items)
		case *PyBytes:
			if n <= 0 {
				return pyBytes(nil)
			}
			checkRepeatBytesLimit(len(rv.v), n)
			result := make([]byte, 0, len(rv.v)*int(n))
			for i := int64(0); i < n; i++ {
				result = append(result, rv.v...)
			}
			return pyBytes(result)
		case *PyInt, *PyBool, *PyFloat:
			// numeric * numeric
			return e.numericMul(left, right)
		}
	case *PyFloat:
		return e.numericMul(left, right)
	}
	// Fall back to numeric mul
	return e.numericMul(left, right)
}

// checkRepeatBytesLimit raises MemoryError if repeating unitLen bytes n times
// would exceed maxRepeatBytes. unitLen==0 is always safe (empty string/bytes).
func checkRepeatBytesLimit(unitLen int, n int64) {
	if unitLen > 0 && n > maxRepeatBytes/int64(unitLen) {
		panic(exceptionSignal{exc: newExceptionf(ExcMemoryError, "repeated string/bytes is too large")})
	}
}

// checkRepeatItemsLimit raises MemoryError if repeating unitLen items n times
// would produce more than maxRepeatBytes/8 items (each Object pointer is 8 bytes).
func checkRepeatItemsLimit(unitLen int, n int64) {
	const maxItems = maxRepeatBytes / 8 // ~128k objects
	if unitLen > 0 && n > maxItems/int64(unitLen) {
		panic(exceptionSignal{exc: newExceptionf(ExcMemoryError, "repeated list/tuple is too large")})
	}
}

func (e *Evaluator) numericMul(left, right Object) Object {
	left = normBool(left)
	right = normBool(right)
	switch lv := left.(type) {
	case *PyInt:
		switch rv := right.(type) {
		case *PyInt:
			result := new(big.Int).Mul(lv.toBigInt(), rv.toBigInt())
			return pyIntBig(result)
		case *PyFloat:
			if n, ok := lv.int64(); ok {
				return pyFloat(float64(n) * rv.v)
			}
		}
	case *PyFloat:
		switch rv := right.(type) {
		case *PyFloat:
			return pyFloat(lv.v * rv.v)
		case *PyInt:
			if n, ok := rv.int64(); ok {
				return pyFloat(lv.v * float64(n))
			}
		}
	}
	raiseTypeError("unsupported operand type(s) for *: '%s' and '%s'", left.pyType().Name, right.pyType().Name)
	return nil
}

func (e *Evaluator) divOp(left, right Object) Object {
	// Python 3: / always returns float
	lf := toFloatVal(left)
	rf := toFloatVal(right)
	if rf == 0 {
		panic(exceptionSignal{exc: newExceptionf(ExcZeroDivisionError, "division by zero")})
	}
	return pyFloat(lf / rf)
}

func (e *Evaluator) floorDivOp(left, right Object) Object {
	left = normBool(left)
	right = normBool(right)
	switch lv := left.(type) {
	case *PyInt:
		switch rv := right.(type) {
		case *PyInt:
			// Use big.Int arithmetic to handle values that don't fit in int64.
			la, ra := lv.toBigInt(), rv.toBigInt()
			if ra.Sign() == 0 {
				panic(exceptionSignal{exc: newExceptionf(ExcZeroDivisionError, "integer division or modulo by zero")})
			}
			q, rem := new(big.Int).DivMod(la, ra, new(big.Int))
			// Python floor division: round toward negative infinity.
			if rem.Sign() != 0 && (rem.Sign() < 0) != (ra.Sign() < 0) {
				q.Sub(q, big.NewInt(1))
			}
			return pyIntBig(q)
		case *PyFloat:
			if n, ok := lv.int64(); ok {
				if rv.v == 0 {
					panic(exceptionSignal{exc: newExceptionf(ExcZeroDivisionError, "float floor division by zero")})
				}
				return pyFloat(math.Floor(float64(n) / rv.v))
			}
			// Big-int operand: convert to float
			f, _ := new(big.Float).SetInt(lv.toBigInt()).Float64()
			if rv.v == 0 {
				panic(exceptionSignal{exc: newExceptionf(ExcZeroDivisionError, "float floor division by zero")})
			}
			return pyFloat(math.Floor(f / rv.v))
		}
	case *PyFloat:
		rf := toFloatVal(right)
		if rf == 0 {
			panic(exceptionSignal{exc: newExceptionf(ExcZeroDivisionError, "float floor division by zero")})
		}
		return pyFloat(math.Floor(lv.v / rf))
	}
	raiseTypeError("unsupported operand type(s) for //: '%s' and '%s'", left.pyType().Name, right.pyType().Name)
	return nil
}

func (e *Evaluator) modOp(left, right Object) Object {
	// str % args: format string
	if ls, ok := left.(*PyStr); ok {
		return pyStr(strPercent(ls.v, right))
	}

	left = normBool(left)
	right = normBool(right)

	switch lv := left.(type) {
	case *PyInt:
		switch rv := right.(type) {
		case *PyInt:
			// Use big.Int arithmetic to handle values that don't fit in int64.
			la, ra := lv.toBigInt(), rv.toBigInt()
			if ra.Sign() == 0 {
				panic(exceptionSignal{exc: newExceptionf(ExcZeroDivisionError, "integer division or modulo by zero")})
			}
			r := new(big.Int).Mod(la, ra)
			// Python: result has same sign as divisor (Mod already does this for big.Int)
			return pyIntBig(r)
		case *PyFloat:
			if n, ok := lv.int64(); ok {
				if rv.v == 0 {
					panic(exceptionSignal{exc: newExceptionf(ExcZeroDivisionError, "float modulo")})
				}
				r := math.Mod(float64(n), rv.v)
				if r != 0 && ((r < 0) != (rv.v < 0)) {
					r += rv.v
				}
				return pyFloat(r)
			}
			// Big-int operand: convert to float
			f, _ := new(big.Float).SetInt(lv.toBigInt()).Float64()
			if rv.v == 0 {
				panic(exceptionSignal{exc: newExceptionf(ExcZeroDivisionError, "float modulo")})
			}
			r := math.Mod(f, rv.v)
			if r != 0 && ((r < 0) != (rv.v < 0)) {
				r += rv.v
			}
			return pyFloat(r)
		}
	case *PyFloat:
		rf := toFloatVal(right)
		if rf == 0 {
			panic(exceptionSignal{exc: newExceptionf(ExcZeroDivisionError, "float modulo")})
		}
		r := math.Mod(lv.v, rf)
		if r != 0 && ((r < 0) != (rf < 0)) {
			r += rf
		}
		return pyFloat(r)
	}
	raiseTypeError("unsupported operand type(s) for %%: '%s' and '%s'", left.pyType().Name, right.pyType().Name)
	return nil
}

func (e *Evaluator) powOp(left, right Object) Object {
	left = normBool(left)
	right = normBool(right)
	switch lv := left.(type) {
	case *PyInt:
		switch rv := right.(type) {
		case *PyInt:
			en, eok := rv.int64()
			if eok && en >= 0 {
				// Guard against exponents that would produce astronomically large results.
				// Analogous to the maxShift cap on <<. Allow up to ~8 Mbit of result.
				const maxExpBits = 8 * maxRepeatBytes
				baseBits := int64(lv.toBigInt().BitLen())
				if baseBits > 1 && en > maxExpBits/baseBits {
					panic(exceptionSignal{exc: newExceptionf(ExcOverflowError, "integer exponentiation result too large")})
				}
				result := new(big.Int).Exp(lv.toBigInt(), rv.toBigInt(), nil)
				return pyIntBig(result)
			}
			// Negative exponent → float
			bn, _ := lv.int64()
			en2, _ := rv.int64()
			return pyFloat(math.Pow(float64(bn), float64(en2)))
		case *PyFloat:
			if n, ok := lv.int64(); ok {
				return pyFloat(math.Pow(float64(n), rv.v))
			}
		}
	case *PyFloat:
		rf := toFloatVal(right)
		return pyFloat(math.Pow(lv.v, rf))
	}
	raiseTypeError("unsupported operand type(s) for **: '%s' and '%s'", left.pyType().Name, right.pyType().Name)
	return nil
}

func (e *Evaluator) bitwiseOp(op string, left, right Object) Object {
	// Set operations: &=intersection, ^=symmetric difference, -=difference
	if ls, ok := left.(*PySet); ok {
		if rs, ok2 := right.(*PySet); ok2 {
			result := &PySet{items: make(map[any]Object)}
			switch op {
			case "&":
				for k, item := range ls.items {
					if _, ok3 := rs.items[k]; ok3 {
						result.items[k] = item
					}
				}
			case "^":
				for k, item := range ls.items {
					if _, ok3 := rs.items[k]; !ok3 {
						result.items[k] = item
					}
				}
				for k, item := range rs.items {
					if _, ok3 := ls.items[k]; !ok3 {
						result.items[k] = item
					}
				}
			case "-":
				for k, item := range ls.items {
					if _, ok3 := rs.items[k]; !ok3 {
						result.items[k] = item
					}
				}
			}
			return result
		}
		raiseTypeError("unsupported operand type(s) for %s: 'set' and '%s'", op, right.pyType().Name)
	}

	left = normBool(left)
	right = normBool(right)
	lv, lok := left.(*PyInt)
	rv, rok := right.(*PyInt)
	if !lok || !rok {
		raiseTypeError("unsupported operand type(s) for %s: '%s' and '%s'", op, left.pyType().Name, right.pyType().Name)
	}
	switch op {
	case "&":
		result := new(big.Int).And(lv.toBigInt(), rv.toBigInt())
		return pyIntBig(result)
	case "^":
		result := new(big.Int).Xor(lv.toBigInt(), rv.toBigInt())
		return pyIntBig(result)
	case "<<":
		rn, rok2 := rv.int64()
		if !rok2 || rn < 0 {
			panic(exceptionSignal{exc: newExceptionf(ExcValueError, "negative shift count")})
		}
		// Cap shift to prevent OOM on huge left shifts (result would exceed output limit anyway).
		const maxShift = 1 << 23 // 8 MB worth of bits
		if rn > maxShift {
			rn = maxShift
		}
		br := new(big.Int).Lsh(lv.toBigInt(), uint(rn))
		return pyIntBig(br)
	case ">>":
		rn, rok2 := rv.int64()
		if !rok2 || rn < 0 {
			panic(exceptionSignal{exc: newExceptionf(ExcValueError, "negative shift count")})
		}
		br := new(big.Int).Rsh(lv.toBigInt(), uint(rn))
		return pyIntBig(br)
	}
	return pyInt(0)
}

func (e *Evaluator) bitwiseOrOp(left, right Object) Object {
	// set | set = union
	if ls, ok := left.(*PySet); ok {
		if rs, ok2 := right.(*PySet); ok2 {
			result := &PySet{items: make(map[any]Object)}
			for k, item := range ls.items {
				result.items[k] = item
			}
			for k, item := range rs.items {
				result.items[k] = item
			}
			return result
		}
		raiseTypeError("unsupported operand type(s) for |: 'set' and '%s'", right.pyType().Name)
	}

	left = normBool(left)
	right = normBool(right)

	// dict | dict (Python 3.9+)
	if ld, ok := left.(*PyDict); ok {
		if rd, ok2 := right.(*PyDict); ok2 {
			newD := pyDict()
			for i, k := range ld.keys {
				newD.set(k, ld.vals[i])
			}
			for i, k := range rd.keys {
				newD.set(k, rd.vals[i])
			}
			return newD
		}
	}

	// set | set
	if ls, ok := left.(*PySet); ok {
		if rs, ok2 := right.(*PySet); ok2 {
			result := &PySet{items: make(map[any]Object)}
			for k, v := range ls.items {
				result.items[k] = v
			}
			for k, v := range rs.items {
				result.items[k] = v
			}
			return result
		}
	}

	// int | int
	lv, lok := left.(*PyInt)
	rv, rok := right.(*PyInt)
	if !lok || !rok {
		raiseTypeError("unsupported operand type(s) for |: '%s' and '%s'", left.pyType().Name, right.pyType().Name)
	}
	result := new(big.Int).Or(lv.toBigInt(), rv.toBigInt())
	return pyIntBig(result)
}

func (e *Evaluator) evalUnaryOp(n *UnaryOp) Object {
	operand := e.eval(n.Operand)
	switch n.Op {
	case "-":
		operand = normBool(operand)
		switch v := operand.(type) {
		case *PyInt:
			result := new(big.Int).Neg(v.toBigInt())
			return pyIntBig(result)
		case *PyFloat:
			return pyFloat(-v.v)
		}
		raiseTypeError("bad operand type for unary -: '%s'", operand.pyType().Name)
	case "+":
		operand = normBool(operand)
		switch v := operand.(type) {
		case *PyInt:
			return v
		case *PyFloat:
			return v
		}
		raiseTypeError("bad operand type for unary +: '%s'", operand.pyType().Name)
	case "~":
		operand = normBool(operand)
		if v, ok := operand.(*PyInt); ok {
			if v.big == nil {
				return pyInt(^v.small)
			}
			result := new(big.Int).Not(v.big)
			return pyIntBig(result)
		}
		raiseTypeError("bad operand type for unary ~: '%s'", operand.pyType().Name)
	case "not":
		return pyBool(!pyTruth(operand))
	}
	return pyNone
}

func (e *Evaluator) evalBoolOp(n *BoolOp) Object {
	if n.Op == "and" {
		var result Object = pyTrue
		for _, val := range n.Values {
			result = e.eval(val)
			if !pyTruth(result) {
				return result
			}
		}
		return result
	}
	// or
	var result Object = pyFalse
	for _, val := range n.Values {
		result = e.eval(val)
		if pyTruth(result) {
			return result
		}
	}
	return result
}

func (e *Evaluator) evalCompare(n *Compare) Object {
	left := e.eval(n.Left)
	for i, op := range n.Ops {
		right := e.eval(n.Comparators[i])
		if !e.compareTwo(op, left, right) {
			return pyFalse
		}
		left = right
	}
	return pyTrue
}

func (e *Evaluator) compareTwo(op string, left, right Object) bool {
	switch op {
	case "==":
		return pyEq(left, right)
	case "!=":
		return !pyEq(left, right)
	case "<":
		return pyCompare(left, right) < 0
	case "<=":
		return pyCompare(left, right) <= 0
	case ">":
		return pyCompare(left, right) > 0
	case ">=":
		return pyCompare(left, right) >= 0
	case "in":
		return e.contains(right, left)
	case "not in":
		return !e.contains(right, left)
	case "is":
		return left == right
	case "is not":
		return left != right
	}
	return false
}

func (e *Evaluator) contains(container, item Object) bool {
	switch c := container.(type) {
	case *PyList:
		for _, v := range c.items {
			if pyEq(v, item) {
				return true
			}
		}
		return false
	case *PyTuple:
		for _, v := range c.items {
			if pyEq(v, item) {
				return true
			}
		}
		return false
	case *PyStr:
		if s, ok := item.(*PyStr); ok {
			return len(s.v) == 0 || containsSubstring(c.v, s.v)
		}
		raiseTypeError("'in <string>' requires string as left operand, not %s", item.pyType().Name)
	case *PyDict:
		k, err := hashKey(item)
		if err != nil {
			return false
		}
		_, ok := c.index[k]
		return ok
	case *PySet:
		k, err := hashKey(item)
		if err != nil {
			return false
		}
		_, ok := c.items[k]
		return ok
	case *PyFrozenSet:
		k, err := hashKey(item)
		if err != nil {
			return false
		}
		_, ok := c.items[k]
		return ok
	case *PyBytes:
		if b, ok := item.(*PyBytes); ok {
			return bytesContains(c.v, b.v)
		}
		if n, ok := item.(*PyInt); ok {
			v, _ := n.int64()
			for _, byt := range c.v {
				if int64(byt) == v {
					return true
				}
			}
			return false
		}
	case *PyRange:
		items := collectIterable(c)
		for _, v := range items {
			if pyEq(v, item) {
				return true
			}
		}
		return false
	case *PyInstance:
		if fn, ok2 := c.lookupMethod("__contains__"); ok2 {
			result := e.callObject(fn, []Object{c, item}, nil)
			return pyTruth(result)
		}
	}
	raiseTypeError("argument of type '%s' is not iterable", container.pyType().Name)
	return false
}

func containsSubstring(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func bytesContains(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func (e *Evaluator) evalCall(n *CallExpr) Object {
	fn := e.eval(n.Func)

	// Collect positional args
	args := make([]Object, 0, len(n.Args))
	for _, arg := range n.Args {
		if st, ok := arg.(*Starred); ok {
			expanded := e.iterateObj(e.eval(st.Value))
			args = append(args, expanded...)
		} else {
			args = append(args, e.eval(arg))
		}
	}

	// Collect keyword args
	kwargs := make(map[string]Object)
	for _, kw := range n.Keywords {
		if kw.Arg == "" {
			// **unpack
			val := e.eval(kw.Value)
			if d, ok := val.(*PyDict); ok {
				for i, k := range d.keys {
					if ks, ok2 := k.(*PyStr); ok2 {
						kwargs[ks.v] = d.vals[i]
					}
				}
			}
		} else {
			kwargs[kw.Arg] = e.eval(kw.Value)
		}
	}

	// Extra *args (from Starargs field)
	for _, sa := range n.Starargs {
		expanded := e.iterateObj(e.eval(sa))
		args = append(args, expanded...)
	}

	// Extra **kwargs
	for _, ka := range n.Kwargs {
		val := e.eval(ka)
		if d, ok := val.(*PyDict); ok {
			for i, k := range d.keys {
				if ks, ok2 := k.(*PyStr); ok2 {
					kwargs[ks.v] = d.vals[i]
				}
			}
		}
	}

	if len(kwargs) == 0 {
		kwargs = nil
	}

	// Special handling for super() — need to wire up __class__ and self
	if isName(n.Func, "super") && len(args) == 0 {
		return e.resolveSuper()
	}

	return e.callObject(fn, args, kwargs)
}

func isName(expr Expr, name string) bool {
	if ne, ok := expr.(*NameExpr); ok {
		return ne.Id == name
	}
	return false
}

func (e *Evaluator) resolveSuper() Object {
	// Walk scope chain to find __class__ and the first arg
	scope := e.scope
	for scope != nil {
		if cls := scope.class; cls != nil {
			// Find 'self' — the first argument of the enclosing function
			// Look for it in the scope vars
			if self, ok2 := scope.vars["self"]; ok2 {
				return &PySuper{Class: cls, Obj: self}
			}
			// Try parent scope
			if scope.parent != nil {
				if self, ok2 := scope.parent.vars["self"]; ok2 {
					return &PySuper{Class: cls, Obj: self}
				}
			}
			return &PySuper{Class: cls}
		}
		scope = scope.parent
	}
	return &PySuper{}
}

func (e *Evaluator) evalAttribute(n *AttributeExpr) Object {
	obj := e.eval(n.Value)
	val, ok := getAttr(obj, n.Attr)
	if !ok {
		raiseAttributeError(obj.pyType().Name, n.Attr)
	}
	return val
}

func (e *Evaluator) evalSubscript(n *SubscriptExpr) Object {
	obj := e.eval(n.Value)

	// Check if it's a slice
	if sl, ok := n.Slice.(*SliceExpr); ok {
		return e.getSlice(obj, sl)
	}

	key := e.eval(n.Slice)
	return e.getItem(obj, key)
}

func (e *Evaluator) getItem(obj Object, key Object) Object {
	switch v := obj.(type) {
	case *PyList:
		idx := int(toIntVal(key))
		idx = normalizeIndex(idx, len(v.items))
		if idx < 0 || idx >= len(v.items) {
			raiseIndexError("list index out of range")
		}
		return v.items[idx]
	case *PyTuple:
		idx := int(toIntVal(key))
		idx = normalizeIndex(idx, len(v.items))
		if idx < 0 || idx >= len(v.items) {
			raiseIndexError("tuple index out of range")
		}
		return v.items[idx]
	case *PyStr:
		runes := []rune(v.v)
		idx := int(toIntVal(key))
		idx = normalizeIndex(idx, len(runes))
		if idx < 0 || idx >= len(runes) {
			raiseIndexError("string index out of range")
		}
		return pyStr(string(runes[idx]))
	case *PyBytes:
		idx := int(toIntVal(key))
		idx = normalizeIndex(idx, len(v.v))
		if idx < 0 || idx >= len(v.v) {
			raiseIndexError("index out of range")
		}
		return pyInt(int64(v.v[idx]))
	case *PyDict:
		val, ok := v.get(key)
		if !ok {
			raiseKeyError(key)
		}
		return val
	case *PyInstance:
		if fn, ok2 := v.lookupMethod("__getitem__"); ok2 {
			return e.callObject(fn, []Object{v, key}, nil)
		}
		raiseTypeError("'%s' object is not subscriptable", v.Class.Name)
	}
	raiseTypeError("'%s' object is not subscriptable", obj.pyType().Name)
	return nil
}

func (e *Evaluator) evalSlice(n *SliceExpr) Object {
	// Returns a PySlice-like tuple: we use a PyTuple with marker
	// Actually return a *PySlice object represented as a special object
	return e.buildSliceObj(n)
}

// pySliceObj is a Python slice object (for use as subscript)
type pySliceObj struct {
	lower, upper, step Object
}

func (s *pySliceObj) pyType() *PyType { return typeSlice }
func (s *pySliceObj) pyRepr() string {
	return fmt.Sprintf("slice(%v, %v, %v)", s.lower, s.upper, s.step)
}
func (s *pySliceObj) pyStr() string { return s.pyRepr() }

func (e *Evaluator) buildSliceObj(n *SliceExpr) *pySliceObj {
	var lower, upper, step Object = pyNone, pyNone, pyNone
	if n.Lower != nil {
		lower = e.eval(n.Lower)
	}
	if n.Upper != nil {
		upper = e.eval(n.Upper)
	}
	if n.Step != nil {
		step = e.eval(n.Step)
	}
	return &pySliceObj{lower: lower, upper: upper, step: step}
}

func (e *Evaluator) getSlice(obj Object, n *SliceExpr) Object {
	sl := e.buildSliceObj(n)

	switch v := obj.(type) {
	case *PyList:
		start, stop, step := resolveSlice(sl, len(v.items))
		return pyList(sliceItems(v.items, start, stop, step))
	case *PyTuple:
		start, stop, step := resolveSlice(sl, len(v.items))
		return pyTuple(sliceItems(v.items, start, stop, step))
	case *PyStr:
		runes := []rune(v.v)
		start, stop, step := resolveSlice(sl, len(runes))
		sliced := sliceRunes(runes, start, stop, step)
		return pyStr(string(sliced))
	case *PyBytes:
		start, stop, step := resolveSlice(sl, len(v.v))
		sliced := sliceBytes(v.v, start, stop, step)
		return pyBytes(sliced)
	case *PyInstance:
		sliceObj := sl
		if fn, ok := v.lookupMethod("__getitem__"); ok {
			return e.callObject(fn, []Object{v, sliceObj}, nil)
		}
	}
	raiseTypeError("'%s' object is not subscriptable", obj.pyType().Name)
	return nil
}

func resolveSlice(sl *pySliceObj, length int) (start, stop, step int) {
	step = 1
	if sl.step != pyNone && sl.step != nil {
		step = int(toIntVal(sl.step))
	}
	if step == 0 {
		panic(exceptionSignal{exc: newExceptionf(ExcValueError, "slice step cannot be zero")})
	}

	if step > 0 {
		start = 0
		stop = length
	} else {
		start = length - 1
		stop = -length - 1
	}

	if sl.lower != pyNone && sl.lower != nil {
		start = int(toIntVal(sl.lower))
		if start < 0 {
			start += length
		}
	}
	if sl.upper != pyNone && sl.upper != nil {
		stop = int(toIntVal(sl.upper))
		if stop < 0 {
			stop += length
		}
	}
	return start, stop, step
}

func sliceItems(items []Object, start, stop, step int) []Object {
	var result []Object
	if step > 0 {
		for i := start; i < stop && i < len(items); i += step {
			if i >= 0 {
				result = append(result, items[i])
			}
		}
	} else {
		for i := start; i > stop && i >= 0; i += step {
			if i < len(items) {
				result = append(result, items[i])
			}
		}
	}
	return result
}

func sliceRunes(runes []rune, start, stop, step int) []rune {
	var result []rune
	if step > 0 {
		for i := start; i < stop && i < len(runes); i += step {
			if i >= 0 {
				result = append(result, runes[i])
			}
		}
	} else {
		for i := start; i > stop && i >= 0; i += step {
			if i < len(runes) {
				result = append(result, runes[i])
			}
		}
	}
	return result
}

func sliceBytes(b []byte, start, stop, step int) []byte {
	var result []byte
	if step > 0 {
		for i := start; i < stop && i < len(b); i += step {
			if i >= 0 {
				result = append(result, b[i])
			}
		}
	} else {
		for i := start; i > stop && i >= 0; i += step {
			if i < len(b) {
				result = append(result, b[i])
			}
		}
	}
	return result
}

func (e *Evaluator) setItem(obj Object, key Object, val Object) {
	switch v := obj.(type) {
	case *PyList:
		// Handle slice assignment
		if sl, ok := key.(*pySliceObj); ok {
			start, stop, step := resolveSlice(sl, len(v.items))
			if step != 1 {
				// Extended slice assignment not fully supported
				raiseTypeError("extended slice assignment not supported")
			}
			newItems := e.iterateObj(val)
			if start < 0 {
				start = 0
			}
			if stop > len(v.items) {
				stop = len(v.items)
			}
			if stop < start {
				stop = start
			}
			result := make([]Object, 0, len(v.items)-(stop-start)+len(newItems))
			result = append(result, v.items[:start]...)
			result = append(result, newItems...)
			result = append(result, v.items[stop:]...)
			v.items = result
			return
		}
		idx := int(toIntVal(key))
		idx = normalizeIndex(idx, len(v.items))
		if idx < 0 || idx >= len(v.items) {
			raiseIndexError("list assignment index out of range")
		}
		v.items[idx] = val
	case *PyDict:
		v.set(key, val)
	case *PyInstance:
		if fn, ok2 := v.lookupMethod("__setitem__"); ok2 {
			e.callObject(fn, []Object{v, key, val}, nil)
			return
		}
		raiseTypeError("'%s' object does not support item assignment", v.Class.Name)
	default:
		raiseTypeError("'%s' object does not support item assignment", obj.pyType().Name)
	}
}

func (e *Evaluator) evalName(n *NameExpr) Object {
	val, ok := e.scope.get(n.Id)
	if !ok {
		// Check globals
		if val2, ok2 := e.globals[n.Id]; ok2 {
			return val2
		}
		raiseNameError(n.Id)
	}
	return val
}

func (e *Evaluator) evalConstant(n *Constant) Object {
	if n.Value == nil {
		return pyNone
	}
	switch v := n.Value.(type) {
	case int64:
		return pyInt(v)
	case float64:
		return pyFloat(v)
	case string:
		return pyStr(v)
	case []byte:
		return pyBytes(v)
	case bool:
		return pyBool(v)
	}
	return pyNone
}

func (e *Evaluator) evalList(n *ListExpr) Object {
	items := make([]Object, 0, len(n.Elts))
	for _, elt := range n.Elts {
		if st, ok := elt.(*Starred); ok {
			expanded := e.iterateObj(e.eval(st.Value))
			items = append(items, expanded...)
		} else {
			items = append(items, e.eval(elt))
		}
	}
	return pyList(items)
}

func (e *Evaluator) evalTuple(n *TupleExpr) Object {
	items := make([]Object, 0, len(n.Elts))
	for _, elt := range n.Elts {
		if st, ok := elt.(*Starred); ok {
			expanded := e.iterateObj(e.eval(st.Value))
			items = append(items, expanded...)
		} else {
			items = append(items, e.eval(elt))
		}
	}
	return pyTuple(items)
}

func (e *Evaluator) evalDict(n *DictExpr) Object {
	d := pyDict()
	for i, keyExpr := range n.Keys {
		valObj := e.eval(n.Values[i])
		if keyExpr == nil {
			// **unpack
			if src, ok := valObj.(*PyDict); ok {
				for j, k := range src.keys {
					d.set(k, src.vals[j])
				}
			}
		} else {
			keyObj := e.eval(keyExpr)
			d.set(keyObj, valObj)
		}
	}
	return d
}

func (e *Evaluator) evalSet(n *SetExpr) Object {
	items := make([]Object, 0, len(n.Elts))
	for _, elt := range n.Elts {
		items = append(items, e.eval(elt))
	}
	s, err := pySet(items)
	if err != nil {
		panic(exceptionSignal{exc: newExceptionf(ExcTypeError, "%v", err)})
	}
	return s
}

func (e *Evaluator) evalIfExp(n *IfExp) Object {
	if pyTruth(e.eval(n.Test)) {
		return e.eval(n.Body)
	}
	return e.eval(n.Orelse)
}

func (e *Evaluator) evalLambda(n *Lambda) Object {
	// Evaluate defaults in current scope
	defaults := make([]Object, len(n.Args.Defaults))
	for i, d := range n.Args.Defaults {
		defaults[i] = e.eval(d)
	}
	// Lambda body is a single expression, wrap in return
	body := []Stmt{&ReturnStmt{Value: n.Body}}
	return &PyFunction{
		Name:     "<lambda>",
		Args:     n.Args,
		Body:     body,
		Closure:  e.scope,
		Globals:  e.globals,
		Defaults: defaults,
	}
}

func (e *Evaluator) evalListComp(n *ListComp) Object {
	items := e.evalComprehension(n.Elt, n.Generators)
	return pyList(items)
}

func (e *Evaluator) evalSetComp(n *SetComp) Object {
	items := e.evalComprehension(n.Elt, n.Generators)
	s, err := pySet(items)
	if err != nil {
		panic(exceptionSignal{exc: newExceptionf(ExcTypeError, "%v", err)})
	}
	return s
}

func (e *Evaluator) evalDictComp(n *DictComp) Object {
	d := pyDict()
	e.evalDictCompHelper(n.Key, n.Value, n.Generators, 0, d)
	return d
}

func (e *Evaluator) evalDictCompHelper(keyExpr, valExpr Expr, gens []*Comprehension, depth int, d *PyDict) {
	if depth >= len(gens) {
		k := e.eval(keyExpr)
		v := e.eval(valExpr)
		d.set(k, v)
		return
	}
	gen := gens[depth]
	items := e.iterateObj(e.eval(gen.Iter))
	for _, item := range items {
		e.assign(gen.Target, item)
		pass := true
		for _, cond := range gen.Ifs {
			if !pyTruth(e.eval(cond)) {
				pass = false
				break
			}
		}
		if pass {
			e.evalDictCompHelper(keyExpr, valExpr, gens, depth+1, d)
		}
	}
}

func (e *Evaluator) evalComprehension(eltExpr Expr, gens []*Comprehension) []Object {
	// Run in a new child scope (list comprehensions have their own scope in Python 3)
	childScope := newFunctionScope(e.scope, e.globals, "<comprehension>")
	child := &Evaluator{
		ctx:     e.ctx,
		scope:   childScope,
		globals: e.globals,
		opts:    e.opts,
		modules: e.modules,
		depth:   e.depth,
	}

	var result []Object
	child.evalCompHelper(eltExpr, gens, 0, &result)
	return result
}

func (e *Evaluator) evalCompHelper(eltExpr Expr, gens []*Comprehension, depth int, result *[]Object) {
	if depth >= len(gens) {
		*result = append(*result, e.eval(eltExpr))
		return
	}
	gen := gens[depth]
	items := e.iterateObj(e.eval(gen.Iter))
	for _, item := range items {
		e.assign(gen.Target, item)
		pass := true
		for _, cond := range gen.Ifs {
			if !pyTruth(e.eval(cond)) {
				pass = false
				break
			}
		}
		if pass {
			e.evalCompHelper(eltExpr, gens, depth+1, result)
		}
	}
}

func (e *Evaluator) evalGeneratorExp(n *GeneratorExp) Object {
	// Eagerly evaluate the first iterator (per Python semantics), create a generator
	if len(n.Generators) == 0 {
		return &PyGenerator{name: "<genexpr>", sendCh: make(chan Object), yieldCh: make(chan Object), ctx: e.ctx}
	}

	// Capture first iterator in current scope
	firstIter := e.eval(n.Generators[0].Iter)

	// Create a fake function body that yields from the comprehension
	// We implement this as a real generator that runs the comprehension
	g := &PyGenerator{
		name:    "<genexpr>",
		sendCh:  make(chan Object, 0),
		yieldCh: make(chan Object, 0),
		excCh:   make(chan *PyException, 1),
		ctx:     e.ctx,
	}

	childScope := newFunctionScope(e.scope, e.globals, "<genexpr>")
	childEval := &Evaluator{
		ctx:     e.ctx,
		scope:   childScope,
		globals: e.globals,
		opts:    e.opts,
		modules: e.modules,
		genState: &genChannels{
			sendCh:  g.sendCh,
			yieldCh: g.yieldCh,
			ctx:     e.ctx,
		},
	}

	// Copy generators but replace first iter with already-evaluated value
	gens := make([]*Comprehension, len(n.Generators))
	copy(gens, n.Generators)
	firstItems := childEval.iterateObj(firstIter)

	go func() {
		// Register this goroutine's callObject so that map/filter/sorted with
		// user-defined key functions work correctly inside generator expressions.
		if gid, ok := goroutineID(); ok {
			goroutineCallFns.Store(gid, func(fn Object, args []Object, kwargs map[string]Object) Object {
				return childEval.callObject(fn, args, kwargs)
			})
			defer goroutineCallFns.Delete(gid)
		}
		defer close(g.yieldCh)
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if sig, ok := r.(exceptionSignal); ok {
				if exceptionMatchesClass(sig.exc, ExcStopIteration) {
					return
				}
				if exceptionMatchesClass(sig.exc, ExcGeneratorExit) {
					return
				}
				// Non-StopIteration Python exception: propagate to the caller via excCh.
				select {
				case g.excCh <- sig.exc:
				default:
				}
				return
			}
			if _, ok := r.(controlSignal); ok {
				// controlSignal for return is normal completion.
				return
			}
			// Real Go panic — re-panic so it is not silently swallowed.
			panic(r)
		}()

		childEval.evalGenExpHelper(n.Elt, firstItems, gens, 0)
	}()

	return g
}

func (e *Evaluator) evalGenExpHelper(eltExpr Expr, firstItems []Object, gens []*Comprehension, depth int) {
	if depth >= len(gens) {
		val := e.eval(eltExpr)
		select {
		case e.genState.yieldCh <- val:
		case <-e.ctx.Done():
			return
		}
		select {
		case _, ok := <-e.genState.sendCh:
			if !ok {
				return
			}
		case <-e.ctx.Done():
			return
		}
		return
	}

	gen := gens[depth]
	var items []Object
	if depth == 0 {
		items = firstItems
	} else {
		items = e.iterateObj(e.eval(gen.Iter))
	}

	for _, item := range items {
		e.assign(gen.Target, item)
		pass := true
		for _, cond := range gen.Ifs {
			if !pyTruth(e.eval(cond)) {
				pass = false
				break
			}
		}
		if pass {
			e.evalGenExpHelper(eltExpr, nil, gens, depth+1)
		}
	}
}

func (e *Evaluator) evalYield(n *Yield) Object {
	if e.genState == nil {
		raiseTypeError("'yield' outside function")
	}
	var val Object = pyNone
	if n.Value != nil {
		val = e.eval(n.Value)
	}
	select {
	case e.genState.yieldCh <- val:
	case <-e.ctx.Done():
		panic(exceptionSignal{exc: newExceptionf(ExcKeyboardInterrupt, "cancelled")})
	}
	select {
	case sent, ok := <-e.genState.sendCh:
		if !ok {
			panic(exceptionSignal{exc: newExceptionf(ExcGeneratorExit, "generator closed")})
		}
		return sent
	case <-e.ctx.Done():
		panic(exceptionSignal{exc: newExceptionf(ExcKeyboardInterrupt, "cancelled")})
	}
}

func (e *Evaluator) evalYieldFrom(n *YieldFrom) Object {
	if e.genState == nil {
		raiseTypeError("'yield from' outside function")
	}
	sub := e.eval(n.Value)
	for {
		val, ok := e.nextFromIter(sub)
		if !ok {
			break
		}
		select {
		case e.genState.yieldCh <- val:
		case <-e.ctx.Done():
			panic(exceptionSignal{exc: newExceptionf(ExcKeyboardInterrupt, "cancelled")})
		}
		select {
		case _, ok2 := <-e.genState.sendCh:
			if !ok2 {
				panic(exceptionSignal{exc: newExceptionf(ExcGeneratorExit, "generator closed")})
			}
		case <-e.ctx.Done():
			panic(exceptionSignal{exc: newExceptionf(ExcKeyboardInterrupt, "cancelled")})
		}
	}
	return pyNone
}

// ---- Function calling ----

// callObject dispatches a call to the appropriate handler.
func (e *Evaluator) callObject(fn Object, args []Object, kwargs map[string]Object) Object {
	switch f := fn.(type) {
	case *PyBuiltin:
		if kwargs == nil {
			kwargs = map[string]Object{}
		}
		return f.Fn(args, kwargs)
	case *PyFunction:
		return e.callFunction(f, args, kwargs)
	case *PyBoundMethod:
		allArgs := make([]Object, 0, 1+len(args))
		allArgs = append(allArgs, f.Self)
		allArgs = append(allArgs, args...)
		return e.callFunction(f.Func, allArgs, kwargs)
	case *PyClass:
		return e.callClass(f, args, kwargs)
	case *PyInstance:
		// __call__
		if meth, ok := f.lookupMethod("__call__"); ok {
			allArgs := make([]Object, 0, 1+len(args))
			allArgs = append(allArgs, f)
			allArgs = append(allArgs, args...)
			return e.callObject(meth, allArgs, kwargs)
		}
		raiseTypeError("'%s' object is not callable", f.Class.Name)
	case *PyType:
		// Built-in type constructors: int, str, float, etc. are registered as PyBuiltin
		// This path should rarely be hit
		raiseTypeError("type '%s' object is not callable this way", f.Name)
	}
	raiseTypeError("'%s' object is not callable", fn.pyType().Name)
	return nil
}

func (e *Evaluator) callClass(cls *PyClass, args []Object, kwargs map[string]Object) Object {
	// If the class is an exception class (subclass of BaseException),
	// instantiate it as a *PyException for proper raise/except semantics.
	if classIsException(cls) {
		exc := &PyException{ExcClass: cls, Args: args, Dict: make(map[string]Object)}
		// Run custom __init__ if present (for user-defined exception classes).
		if initFn, ok := cls.lookupInMRO("__init__"); ok {
			// Wrap exc in an adapter so __init__ can set attributes on it.
			allArgs := make([]Object, 0, 1+len(args))
			allArgs = append(allArgs, exc)
			allArgs = append(allArgs, args...)
			e.callObject(initFn, allArgs, kwargs)
		}
		return exc
	}

	inst := &PyInstance{Class: cls, Dict: make(map[string]Object)}

	// Look up __init__ in MRO
	if initFn, ok := cls.lookupInMRO("__init__"); ok {
		allArgs := make([]Object, 0, 1+len(args))
		allArgs = append(allArgs, inst)
		allArgs = append(allArgs, args...)
		e.callObject(initFn, allArgs, kwargs)
	}
	return inst
}

// classIsException returns true if cls is a built-in exception class (one of the
// ExcBaseException singletons or a subclass thereof in the singleton hierarchy).
func classIsException(cls *PyClass) bool {
	for _, c := range cls.MRO {
		if c == ExcBaseException {
			return true
		}
	}
	return false
}

// lookupInMRO looks for a method in the class MRO.
func (cls *PyClass) lookupInMRO(name string) (Object, bool) {
	for _, c := range cls.MRO {
		if v, ok := c.Dict[name]; ok {
			return v, true
		}
	}
	return nil, false
}

func (e *Evaluator) callFunction(fn *PyFunction, args []Object, kwargs map[string]Object) Object {
	if e.depth >= maxCallDepth {
		panic(exceptionSignal{exc: newExceptionf(ExcRecursionError, "maximum recursion depth exceeded")})
	}

	// Build function scope as child of closure (not current scope — lexical scoping)
	funcScope := newFunctionScope(fn.Closure, fn.Globals, fn.Name)

	// Match args to parameters
	e.bindArgs(fn, funcScope, args, kwargs)

	if fn.IsGen {
		return e.makeGenerator(fn, funcScope)
	}

	// Execute function body
	child := &Evaluator{
		ctx:     e.ctx,
		scope:   funcScope,
		globals: fn.Globals,
		opts:    e.opts,
		modules: e.modules,
		depth:   e.depth + 1,
	}

	var retVal Object = pyNone
	func() {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if sig, ok := r.(controlSignal); ok && sig.kind == ctrlReturn {
				retVal = sig.value
				return
			}
			panic(r)
		}()
		child.exec(fn.Body)
	}()
	return retVal
}

func (e *Evaluator) bindArgs(fn *PyFunction, scope *Scope, args []Object, kwargs map[string]Object) {
	params := fn.Args
	nRequired := len(params.Args) - len(fn.Defaults)

	posIdx := 0
	for i, param := range params.Args {
		if posIdx < len(args) {
			scope.set(param, args[posIdx])
			posIdx++
		} else if kv, ok := kwargs[param]; ok {
			scope.set(param, kv)
			delete(kwargs, param)
		} else if i >= nRequired {
			// Has default
			defIdx := i - nRequired
			scope.set(param, fn.Defaults[defIdx])
		} else {
			panic(exceptionSignal{exc: newExceptionf(ExcTypeError, "%s() missing required argument: '%s'", fn.Name, param)})
		}
	}

	// Handle *args
	if params.Vararg != "" {
		varargs := make([]Object, 0)
		for posIdx < len(args) {
			varargs = append(varargs, args[posIdx])
			posIdx++
		}
		scope.set(params.Vararg, pyTuple(varargs))
	} else if posIdx < len(args) {
		panic(exceptionSignal{exc: newExceptionf(ExcTypeError, "%s() takes %d positional argument(s) but %d were given", fn.Name, len(params.Args), len(args))})
	}

	// Keyword-only args
	for i, kw := range params.KwOnly {
		if kv, ok := kwargs[kw]; ok {
			scope.set(kw, kv)
			delete(kwargs, kw)
		} else if fn.KwDefaults != nil {
			if def, ok2 := fn.KwDefaults[kw]; ok2 {
				scope.set(kw, def)
			} else if i < len(fn.Args.KwDefaults) && fn.Args.KwDefaults[i] == nil {
				panic(exceptionSignal{exc: newExceptionf(ExcTypeError, "%s() missing keyword-only argument: '%s'", fn.Name, kw)})
			}
		}
	}

	// Handle **kwargs
	if params.Kwarg != "" {
		kwargsDict := pyDict()
		for k, v := range kwargs {
			kwargsDict.set(pyStr(k), v)
		}
		scope.set(params.Kwarg, kwargsDict)
	} else if len(kwargs) > 0 {
		// Report first unexpected kwarg
		for k := range kwargs {
			panic(exceptionSignal{exc: newExceptionf(ExcTypeError, "%s() got an unexpected keyword argument '%s'", fn.Name, k)})
		}
	}
}

func (e *Evaluator) makeGenerator(fn *PyFunction, scope *Scope) *PyGenerator {
	g := &PyGenerator{
		name:    fn.Name,
		sendCh:  make(chan Object, 0),
		yieldCh: make(chan Object, 0),
		excCh:   make(chan *PyException, 1),
		ctx:     e.ctx,
	}

	childEval := &Evaluator{
		ctx:     e.ctx,
		scope:   scope,
		globals: fn.Globals,
		opts:    e.opts,
		modules: e.modules,
		depth:   e.depth + 1,
		genState: &genChannels{
			sendCh:  g.sendCh,
			yieldCh: g.yieldCh,
			ctx:     e.ctx,
		},
	}

	go func() {
		// Register this goroutine's callObject so that map/filter/sorted with
		// user-defined key functions work correctly inside generator bodies.
		if gid, ok := goroutineID(); ok {
			goroutineCallFns.Store(gid, func(fn Object, args []Object, kwargs map[string]Object) Object {
				return childEval.callObject(fn, args, kwargs)
			})
			defer goroutineCallFns.Delete(gid)
		}
		defer close(g.yieldCh)
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if sig, ok := r.(exceptionSignal); ok {
				if exceptionMatchesClass(sig.exc, ExcStopIteration) {
					return
				}
				if exceptionMatchesClass(sig.exc, ExcGeneratorExit) {
					return
				}
				// Non-StopIteration Python exception: propagate to the caller of
				// next(g) via excCh so it is not silently swallowed.
				select {
				case g.excCh <- sig.exc:
				default:
				}
				return
			}
			if _, ok := r.(controlSignal); ok {
				// return from generator is normal completion.
				return
			}
			// Real Go panic (nil pointer, index OOB, etc.) — re-panic so it is
			// not silently swallowed.
			panic(r)
		}()
		childEval.exec(fn.Body)
	}()

	return g
}

// ---- Iteration helpers ----

// iterateObj materializes an iterable into a slice.
func (e *Evaluator) iterateObj(obj Object) []Object {
	switch v := obj.(type) {
	case *PyInstance:
		if fn, ok := v.lookupMethod("__iter__"); ok {
			iterObj := e.callObject(fn, []Object{v}, nil)
			return e.drainIter(iterObj)
		}
		if fn, ok := v.lookupMethod("__getitem__"); ok {
			// Legacy iteration protocol
			var items []Object
			for i := 0; ; i++ {
				func() {
					defer func() {
						r := recover()
						if r != nil {
							if sig, ok2 := r.(exceptionSignal); ok2 {
								if exceptionMatchesClass(sig.exc, ExcIndexError) || exceptionMatchesClass(sig.exc, ExcStopIteration) {
									items = nil // sentinel to stop
									return
								}
							}
							panic(r)
						}
					}()
					val := e.callObject(fn, []Object{v, pyInt(int64(i))}, nil)
					items = append(items, val)
				}()
				if items == nil {
					break
				}
			}
			if items == nil {
				return []Object{}
			}
			return items
		}
		raiseTypeError("'%s' object is not iterable", v.Class.Name)
	case *PyListIter:
		var result []Object
		for {
			item, ok := v.next()
			if !ok {
				break
			}
			result = append(result, item)
		}
		return result
	case *PyDictKeyIter:
		var result []Object
		for {
			item, ok := v.next()
			if !ok {
				break
			}
			result = append(result, item)
		}
		return result
	case *rangeIter:
		var result []Object
		for {
			item, ok := v.next()
			if !ok {
				break
			}
			result = append(result, item)
		}
		return result
	}
	return collectIterable(obj)
}

func (e *Evaluator) drainIter(iterObj Object) []Object {
	var result []Object
	for {
		val, ok := e.nextFromIter(iterObj)
		if !ok {
			break
		}
		result = append(result, val)
		if len(result) > maxGeneratorItems {
			panic(exceptionSignal{exc: newExceptionf(ExcMemoryError, "iterable produced too many items (limit %d)", maxGeneratorItems)})
		}
	}
	return result
}

// nextFromIter advances an iterator by one step.
func (e *Evaluator) nextFromIter(obj Object) (Object, bool) {
	switch v := obj.(type) {
	case *rangeIter:
		return v.next()
	case *PyMapIter:
		return v.next()
	case *PyFilterIter:
		return v.next()
	case *PyZipIter:
		return v.next()
	case *PyEnumerateIter:
		return v.next()
	case *PyReversedIter:
		return v.next()
	case *PyListIter:
		return v.next()
	case *PyDictKeyIter:
		return v.next()
	case *PyGenerator:
		return e.nextFromGenerator(v)
	case *PyInstance:
		if fn, ok := v.lookupMethod("__next__"); ok {
			var val Object
			done := false
			func() {
				defer func() {
					r := recover()
					if r == nil {
						return
					}
					if sig, ok2 := r.(exceptionSignal); ok2 {
						if exceptionMatchesClass(sig.exc, ExcStopIteration) {
							done = true
							return
						}
					}
					panic(r)
				}()
				val = e.callObject(fn, []Object{v}, nil)
			}()
			if done {
				return nil, false
			}
			return val, true
		}
		if fn, ok := v.lookupMethod("__iter__"); ok {
			iterObj := e.callObject(fn, []Object{v}, nil)
			return e.nextFromIter(iterObj)
		}
	}
	return nextFromIterable(obj)
}

func (e *Evaluator) nextFromGenerator(g *PyGenerator) (Object, bool) {
	if g.done {
		return nil, false
	}
	// If the generator is waiting for a sendCh kick (it has yielded and is
	// blocked on sendCh), send None to advance it before receiving the next value.
	if g.awaitingSend {
		select {
		case g.sendCh <- pyNone:
			g.awaitingSend = false
		case <-e.ctx.Done():
			g.done = true
			return nil, false
		}
	}
	select {
	case val, ok := <-g.yieldCh:
		if !ok {
			g.done = true
			// Check if the generator exited with a non-StopIteration exception.
			// The generator goroutine sends the exception on excCh before closing
			// yieldCh (via defer), so by the time we see the channel close the
			// exception (if any) is already in excCh.
			if g.excCh != nil {
				select {
				case exc := <-g.excCh:
					panic(exceptionSignal{exc: exc})
				default:
				}
			}
			return nil, false
		}
		g.awaitingSend = true
		return val, true
	case <-e.ctx.Done():
		g.done = true
		return nil, false
	}
}

// callMethod calls a named method on an object, returning the result.
func (e *Evaluator) callMethod(obj Object, name string, args []Object, kwargs map[string]Object) Object {
	val, ok := getAttr(obj, name)
	if !ok {
		raiseAttributeError(obj.pyType().Name, name)
	}
	// If it's a bound method or function in a class, prepend self
	switch fn := val.(type) {
	case *PyFunction:
		allArgs := make([]Object, 0, 1+len(args))
		allArgs = append(allArgs, obj)
		allArgs = append(allArgs, args...)
		return e.callFunction(fn, allArgs, kwargs)
	default:
		return e.callObject(fn, args, kwargs)
	}
}

// ---- Assignment helpers ----

func (e *Evaluator) assign(target Expr, value Object) {
	switch t := target.(type) {
	case *NameExpr:
		e.scope.set(t.Id, value)
	case *AttributeExpr:
		obj := e.eval(t.Value)
		setAttr(obj, t.Attr, value)
	case *SubscriptExpr:
		obj := e.eval(t.Value)
		if sl, ok := t.Slice.(*SliceExpr); ok {
			key := e.buildSliceObj(sl)
			e.setItem(obj, key, value)
		} else {
			key := e.eval(t.Slice)
			e.setItem(obj, key, value)
		}
	case *Starred:
		raiseTypeError("starred assignment target must be in a list or tuple")
	case *TupleExpr:
		e.unpackAssign(t.Elts, value)
	case *ListExpr:
		e.unpackAssign(t.Elts, value)
	}
}

func (e *Evaluator) unpackAssign(elts []Expr, value Object) {
	items := e.iterateObj(value)

	// Find starred position
	starIdx := -1
	for i, elt := range elts {
		if _, ok := elt.(*Starred); ok {
			starIdx = i
			break
		}
	}

	if starIdx == -1 {
		if len(items) != len(elts) {
			if len(items) < len(elts) {
				panic(exceptionSignal{exc: newExceptionf(ExcValueError, "not enough values to unpack (expected %d, got %d)", len(elts), len(items))})
			}
			panic(exceptionSignal{exc: newExceptionf(ExcValueError, "too many values to unpack (expected %d)", len(elts))})
		}
		for i, elt := range elts {
			e.assign(elt, items[i])
		}
	} else {
		before := elts[:starIdx]
		after := elts[starIdx+1:]
		minLen := len(before) + len(after)
		if len(items) < minLen {
			panic(exceptionSignal{exc: newExceptionf(ExcValueError, "not enough values to unpack")})
		}
		for i, elt := range before {
			e.assign(elt, items[i])
		}
		starItems := items[len(before) : len(items)-len(after)]
		e.assign(elts[starIdx].(*Starred).Value, pyList(starItems))
		for i, elt := range after {
			e.assign(elt, items[len(items)-len(after)+i])
		}
	}
}

// ---- Utility helpers ----

// normBool converts *PyBool to *PyInt for arithmetic.
func normBool(obj Object) Object {
	if b, ok := obj.(*PyBool); ok {
		if b.v {
			return pyInt(1)
		}
		return pyInt(0)
	}
	return obj
}

// toFloatVal converts any numeric to float64.
func toFloatVal(obj Object) float64 {
	switch v := obj.(type) {
	case *PyFloat:
		return v.v
	case *PyInt:
		if n, ok := v.int64(); ok {
			return float64(n)
		}
		f, _ := new(big.Float).SetInt(v.big).Float64()
		return f
	case *PyBool:
		if v.v {
			return 1
		}
		return 0
	}
	raiseTypeError("must be real number, not '%s'", obj.pyType().Name)
	return 0
}

// toOptInt tries to extract an int64 from an int-like object.
func toOptInt(obj Object) (int64, bool) {
	switch v := obj.(type) {
	case *PyInt:
		if n, ok := v.int64(); ok {
			return n, true
		}
	case *PyBool:
		if v.v {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// printTraceback prints a Python traceback to w.
func printTraceback(w io.Writer, exc *PyException) {
	fmt.Fprintln(w, "Traceback (most recent call last):")
	for _, frame := range exc.Traceback {
		fmt.Fprintf(w, "  File %q, line %d, in %s\n", frame.File, frame.Line, frame.Name)
	}
	msg := exc.pyStr()
	if msg != "" {
		fmt.Fprintf(w, "%s: %s\n", exc.ExcClass.Name, msg)
	} else {
		fmt.Fprintf(w, "%s\n", exc.ExcClass.Name)
	}

	if exc.Cause != nil {
		fmt.Fprintf(w, "\nThe above exception was the direct cause of the following exception:\n\n")
		printTraceback(w, exc.Cause)
	} else if exc.Context != nil {
		fmt.Fprintf(w, "\nDuring handling of the above exception, another exception occurred:\n\n")
		printTraceback(w, exc.Context)
	}
}
