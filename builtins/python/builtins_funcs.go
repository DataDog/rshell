// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package python

import (
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// makeBuiltins returns the dict of all Python built-in functions and constants.
func makeBuiltins(opts *RunOpts) map[string]Object {
	b := map[string]Object{
		// Constants
		"True":  pyTrue,
		"False": pyFalse,
		"None":  pyNone,

		// Exception classes
		"BaseException":       ExcBaseException,
		"Exception":           ExcException,
		"ArithmeticError":     ExcArithmeticError,
		"LookupError":         ExcLookupError,
		"ValueError":          ExcValueError,
		"TypeError":           ExcTypeError,
		"AttributeError":      ExcAttributeError,
		"NameError":           ExcNameError,
		"ImportError":         ExcImportError,
		"IndexError":          ExcIndexError,
		"KeyError":            ExcKeyError,
		"StopIteration":       ExcStopIteration,
		"GeneratorExit":       ExcGeneratorExit,
		"RuntimeError":        ExcRuntimeError,
		"NotImplementedError": ExcNotImplementedError,
		"OSError":             ExcOSError,
		"IOError":             ExcIOError,
		"FileNotFoundError":   ExcFileNotFoundError,
		"PermissionError":     ExcPermissionError,
		"ZeroDivisionError":   ExcZeroDivisionError,
		"OverflowError":       ExcOverflowError,
		"MemoryError":         ExcMemoryError,
		"KeyboardInterrupt":   ExcKeyboardInterrupt,
		"SystemExit":          ExcSystemExit,
		"AssertionError":      ExcAssertionError,
		"UnboundLocalError":   ExcUnboundLocalError,
		"RecursionError":      ExcRecursionError,
		"UnicodeError":        ExcUnicodeError,
		"UnicodeDecodeError":  ExcUnicodeDecodeError,
		"UnicodeEncodeError":  ExcUnicodeEncodeError,

		// Special singletons
		"NotImplemented": &PyNotImplemented{},
		"Ellipsis":       &PyEllipsis{},

		// Built-in functions
		"print":      makeBuiltinPrint(opts),
		"len":        makeBuiltinLen(),
		"range":      makeBuiltinRange(),
		"zip":        makeBuiltinZip(),
		"map":        makeBuiltinMap(),
		"filter":     makeBuiltinFilter(),
		"enumerate":  makeBuiltinEnumerate(),
		"sorted":     makeBuiltinSorted(),
		"reversed":   makeBuiltinReversed(),
		"all":        makeBuiltinAll(),
		"any":        makeBuiltinAny(),
		"sum":        makeBuiltinSum(),
		"min":        makeBuiltinMin(),
		"max":        makeBuiltinMax(),
		"abs":        makeBuiltinAbs(),
		"divmod":     makeBuiltinDivmod(),
		"pow":        makeBuiltinPow(),
		"round":      makeBuiltinRound(),
		"chr":        makeBuiltinChr(),
		"ord":        makeBuiltinOrd(),
		"bin":        makeBuiltinBin(),
		"hex":        makeBuiltinHex(),
		"oct":        makeBuiltinOct(),
		"getattr":    makeBuiltinGetattr(),
		"setattr":    makeBuiltinSetattr(),
		"hasattr":    makeBuiltinHasattr(),
		"delattr":    makeBuiltinDelattr(),
		"isinstance": makeBuiltinIsinstance(),
		"issubclass": makeBuiltinIssubclass(),
		"type":       makeBuiltinType(),
		"int":        makeBuiltinInt(),
		"str":        makeBuiltinStr(),
		"float":      makeBuiltinFloat(),
		"bool":       makeBuiltinBool(),
		"list":       makeBuiltinList(),
		"dict":       makeBuiltinDict(),
		"tuple":      makeBuiltinTuple(),
		"set":        makeBuiltinSet(),
		"frozenset":  makeBuiltinFrozenset(),
		"repr":       makeBuiltinRepr(),
		"hash":       makeBuiltinHash(),
		"id":         makeBuiltinId(),
		"callable":   makeBuiltinCallable(),
		"next":       makeBuiltinNext(),
		"iter":       makeBuiltinIter(),
		"input":      makeBuiltinInput(opts),
		"vars":       makeBuiltinVars(),
		"dir":        makeBuiltinDir(),
		"format":     makeBuiltinFormat(),
		"bytes":      makeBuiltinBytes(),
		"bytearray":  makeBuiltinBytearray(),
		"memoryview": makeBuiltinMemoryview(),
		"open":       makeBuiltinOpen(opts),
		"super":      makeBuiltinSuper(),
		"object":     makeBuiltinObject(),
		"staticmethod": makeBuiltin("staticmethod", func(args []Object, kwargs map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("staticmethod() takes exactly 1 argument")
			}
			return args[0]
		}),
		"classmethod": makeBuiltin("classmethod", func(args []Object, kwargs map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("classmethod() takes exactly 1 argument")
			}
			return args[0]
		}),
		"property": makeBuiltin("property", func(args []Object, kwargs map[string]Object) Object {
			// Simplified: just return the getter
			if len(args) > 0 {
				return args[0]
			}
			return pyNone
		}),
	}
	return b
}

// ---- Singleton types ----

type PyNotImplemented struct{}

func (p *PyNotImplemented) pyType() *PyType { return typeBuiltin }
func (p *PyNotImplemented) pyRepr() string  { return "NotImplemented" }
func (p *PyNotImplemented) pyStr() string   { return "NotImplemented" }

type PyEllipsis struct{}

func (p *PyEllipsis) pyType() *PyType { return typeBuiltin }
func (p *PyEllipsis) pyRepr() string  { return "Ellipsis" }
func (p *PyEllipsis) pyStr() string   { return "..." }

// ---- Built-in function implementations ----

func makeBuiltinPrint(opts *RunOpts) *PyBuiltin {
	return makeBuiltin("print", func(args []Object, kwargs map[string]Object) Object {
		sep := " "
		end := "\n"
		var out io.Writer = opts.Stdout

		if v, ok := kwargs["sep"]; ok {
			if v == pyNone {
				sep = " "
			} else if s, ok2 := v.(*PyStr); ok2 {
				sep = s.v
			}
		}
		if v, ok := kwargs["end"]; ok {
			if v == pyNone {
				end = "\n"
			} else if s, ok2 := v.(*PyStr); ok2 {
				end = s.v
			}
		}
		if v, ok := kwargs["file"]; ok && v != pyNone {
			if f, ok2 := v.(*PyFile); ok2 {
				if f.w != nil {
					out = f.w
				} else if f.rc != nil {
					// Files opened via open() are read-only; block writes at the
					// application layer for consistency with file.write().
					panic(exceptionSignal{exc: newExceptionf(ExcPermissionError, "print() cannot write to a file opened in read mode")})
				}
			}
		}

		parts := make([]string, len(args))
		for i, arg := range args {
			parts[i] = arg.pyStr()
		}
		fmt.Fprint(out, strings.Join(parts, sep)+end)
		return pyNone
	})
}

func makeBuiltinLen() *PyBuiltin {
	return makeBuiltin("len", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("len() takes exactly 1 argument (%d given)", len(args))
		}
		obj := args[0]
		switch v := obj.(type) {
		case *PyStr:
			return pyInt(int64(utf8.RuneCountInString(v.v)))
		case *PyBytes:
			return pyInt(int64(len(v.v)))
		case *PyList:
			return pyInt(int64(len(v.items)))
		case *PyTuple:
			return pyInt(int64(len(v.items)))
		case *PyDict:
			return pyInt(int64(len(v.keys)))
		case *PySet:
			return pyInt(int64(len(v.items)))
		case *PyFrozenSet:
			return pyInt(int64(len(v.items)))
		case *PyRange:
			return pyInt(v.length())
		case *PyInstance:
			if fn, ok := v.lookupMethod("__len__"); ok {
				result := callObject(fn, []Object{v}, nil)
				return result
			}
		}
		raiseTypeError("object of type '%s' has no len()", obj.pyType().Name)
		return nil
	})
}

func makeBuiltinRange() *PyBuiltin {
	return makeBuiltin("range", func(args []Object, kwargs map[string]Object) Object {
		switch len(args) {
		case 1:
			stop := toIntVal(args[0])
			return &PyRange{start: 0, stop: stop, step: 1}
		case 2:
			start := toIntVal(args[0])
			stop := toIntVal(args[1])
			return &PyRange{start: start, stop: stop, step: 1}
		case 3:
			start := toIntVal(args[0])
			stop := toIntVal(args[1])
			step := toIntVal(args[2])
			if step == 0 {
				raiseValueError("range() arg 3 must not be zero")
			}
			return &PyRange{start: start, stop: stop, step: step}
		default:
			raiseTypeError("range() takes 1 to 3 arguments (%d given)", len(args))
		}
		return nil
	})
}

func makeBuiltinZip() *PyBuiltin {
	return makeBuiltin("zip", func(args []Object, kwargs map[string]Object) Object {
		if len(args) == 0 {
			return &PyZipIter{items: [][]Object{}}
		}
		collected := make([][]Object, len(args))
		for i, arg := range args {
			collected[i] = collectIterable(arg)
		}
		return &PyZipIter{items: collected}
	})
}

func makeBuiltinMap() *PyBuiltin {
	return makeBuiltin("map", func(args []Object, kwargs map[string]Object) Object {
		if len(args) < 2 {
			raiseTypeError("map() requires at least 2 arguments")
		}
		fn := args[0]
		collected := make([][]Object, len(args)-1)
		for i, arg := range args[1:] {
			collected[i] = collectIterable(arg)
		}
		return &PyMapIter{fn: fn, items: collected}
	})
}

func makeBuiltinFilter() *PyBuiltin {
	return makeBuiltin("filter", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 2 {
			raiseTypeError("filter() takes exactly 2 arguments (%d given)", len(args))
		}
		fn := args[0]
		items := collectIterable(args[1])
		return &PyFilterIter{fn: fn, items: items}
	})
}

func makeBuiltinEnumerate() *PyBuiltin {
	return makeBuiltin("enumerate", func(args []Object, kwargs map[string]Object) Object {
		if len(args) < 1 {
			raiseTypeError("enumerate() requires at least 1 argument")
		}
		start := int64(0)
		if len(args) > 1 {
			start = toIntVal(args[1])
		}
		if v, ok := kwargs["start"]; ok {
			start = toIntVal(v)
		}
		items := collectIterable(args[0])
		return &PyEnumerateIter{items: items, counter: start}
	})
}

func makeBuiltinSorted() *PyBuiltin {
	return makeBuiltin("sorted", func(args []Object, kwargs map[string]Object) Object {
		if len(args) < 1 {
			raiseTypeError("sorted() requires at least 1 argument")
		}
		items := collectIterable(args[0])
		result := make([]Object, len(items))
		copy(result, items)
		reverse := false
		var keyFn Object
		if v, ok := kwargs["reverse"]; ok {
			reverse = pyTruth(v)
		}
		if v, ok := kwargs["key"]; ok && v != pyNone {
			keyFn = v
		}
		sortList(result, keyFn, reverse)
		return pyList(result)
	})
}

func makeBuiltinReversed() *PyBuiltin {
	return makeBuiltin("reversed", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("reversed() takes exactly 1 argument")
		}
		items := collectIterable(args[0])
		return &PyReversedIter{items: items, idx: len(items) - 1}
	})
}

func makeBuiltinAll() *PyBuiltin {
	return makeBuiltin("all", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("all() takes exactly 1 argument")
		}
		items := collectIterable(args[0])
		for _, item := range items {
			if !pyTruth(item) {
				return pyFalse
			}
		}
		return pyTrue
	})
}

func makeBuiltinAny() *PyBuiltin {
	return makeBuiltin("any", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("any() takes exactly 1 argument")
		}
		items := collectIterable(args[0])
		for _, item := range items {
			if pyTruth(item) {
				return pyTrue
			}
		}
		return pyFalse
	})
}

func makeBuiltinSum() *PyBuiltin {
	return makeBuiltin("sum", func(args []Object, kwargs map[string]Object) Object {
		if len(args) < 1 {
			raiseTypeError("sum() requires at least 1 argument")
		}
		start := Object(pyInt(0))
		if len(args) > 1 {
			start = args[1]
		}
		items := collectIterable(args[0])
		result := start
		for _, item := range items {
			result = pyAdd(result, item)
		}
		return result
	})
}

func makeBuiltinMin() *PyBuiltin {
	return makeBuiltin("min", func(args []Object, kwargs map[string]Object) Object {
		var keyFn Object
		if v, ok := kwargs["key"]; ok && v != pyNone {
			keyFn = v
		}
		var items []Object
		if len(args) == 1 {
			items = collectIterable(args[0])
		} else {
			items = args
		}
		if len(items) == 0 {
			raiseValueError("min() arg is an empty sequence")
		}
		best := items[0]
		bestKey := applyKey(best, keyFn)
		for _, item := range items[1:] {
			k := applyKey(item, keyFn)
			if pyCompare(k, bestKey) < 0 {
				best = item
				bestKey = k
			}
		}
		return best
	})
}

func makeBuiltinMax() *PyBuiltin {
	return makeBuiltin("max", func(args []Object, kwargs map[string]Object) Object {
		var keyFn Object
		if v, ok := kwargs["key"]; ok && v != pyNone {
			keyFn = v
		}
		var items []Object
		if len(args) == 1 {
			items = collectIterable(args[0])
		} else {
			items = args
		}
		if len(items) == 0 {
			raiseValueError("max() arg is an empty sequence")
		}
		best := items[0]
		bestKey := applyKey(best, keyFn)
		for _, item := range items[1:] {
			k := applyKey(item, keyFn)
			if pyCompare(k, bestKey) > 0 {
				best = item
				bestKey = k
			}
		}
		return best
	})
}

func applyKey(item Object, keyFn Object) Object {
	if keyFn == nil || keyFn == pyNone {
		return item
	}
	return callObject(keyFn, []Object{item}, nil)
}

func makeBuiltinAbs() *PyBuiltin {
	return makeBuiltin("abs", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("abs() takes exactly 1 argument")
		}
		switch v := args[0].(type) {
		case *PyInt:
			if v.big != nil {
				b := new(big.Int).Abs(v.big)
				return pyIntBig(b)
			}
			if v.small < 0 {
				return pyInt(-v.small)
			}
			return v
		case *PyFloat:
			return pyFloat(math.Abs(v.v))
		case *PyBool:
			if !v.v {
				return pyInt(0)
			}
			return pyInt(1)
		}
		raiseTypeError("bad operand type for abs(): '%s'", args[0].pyType().Name)
		return nil
	})
}

func makeBuiltinDivmod() *PyBuiltin {
	return makeBuiltin("divmod", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 2 {
			raiseTypeError("divmod() takes exactly 2 arguments")
		}
		a, b := args[0], args[1]
		switch av := a.(type) {
		case *PyInt:
			if bv, ok := b.(*PyInt); ok {
				// Use big.Int arithmetic to handle values outside int64 range.
				ab := av.toBigInt()
				bb := bv.toBigInt()
				if bb.Sign() == 0 {
					panic(exceptionSignal{exc: newExceptionf(ExcZeroDivisionError, "integer division or modulo by zero")})
				}
				q := new(big.Int)
				r := new(big.Int)
				q.DivMod(ab, bb, r)
				// Python-style modulo: result has same sign as divisor
				if r.Sign() != 0 && r.Sign() != bb.Sign() {
					r.Add(r, bb)
					q.Sub(q, big.NewInt(1))
				}
				return pyTuple([]Object{pyIntBig(q), pyIntBig(r)})
			}
		case *PyFloat:
			var bv float64
			switch bval := b.(type) {
			case *PyFloat:
				bv = bval.v
			case *PyInt:
				if n, ok := bval.int64(); ok {
					bv = float64(n)
				}
			}
			if bv == 0 {
				panic(exceptionSignal{exc: newExceptionf(ExcZeroDivisionError, "float divmod()")})
			}
			q := math.Floor(av.v / bv)
			r := av.v - q*bv
			return pyTuple([]Object{pyFloat(q), pyFloat(r)})
		}
		raiseTypeError("unsupported operand type(s) for divmod()")
		return nil
	})
}

func makeBuiltinPow() *PyBuiltin {
	return makeBuiltin("pow", func(args []Object, kwargs map[string]Object) Object {
		if len(args) < 2 || len(args) > 3 {
			raiseTypeError("pow() takes 2 or 3 arguments (%d given)", len(args))
		}
		base, exp := args[0], args[1]
		if len(args) == 3 {
			// Modular exponentiation
			mod := args[2]
			bi := toIntValObj(base)
			ei := toIntValObj(exp)
			mi := toIntValObj(mod)
			result := new(big.Int).Exp(bi, ei, mi)
			return pyIntBig(result)
		}
		// Regular pow
		switch bv := base.(type) {
		case *PyInt:
			switch ev := exp.(type) {
			case *PyInt:
				en, eok := ev.int64()
				if eok && en >= 0 {
					// Guard against exponents that would produce astronomically large results.
					// Analogous to the maxShift cap on <<. Allow up to ~8 Mbit of result.
					const maxExpBits = 8 * maxRepeatBytes
					baseBits := int64(bv.toBigInt().BitLen())
					if baseBits > 1 && en > maxExpBits/baseBits {
						panic(exceptionSignal{exc: newExceptionf(ExcOverflowError, "integer exponentiation result too large")})
					}
					bi := bv.toBigInt()
					ei := ev.toBigInt()
					result := new(big.Int).Exp(bi, ei, nil)
					return pyIntBig(result)
				}
				// Negative exponent → float
				bn, _ := bv.int64()
				en2, _ := ev.int64()
				return pyFloat(math.Pow(float64(bn), float64(en2)))
			case *PyFloat:
				bn, _ := bv.int64()
				return pyFloat(math.Pow(float64(bn), ev.v))
			}
		case *PyFloat:
			var ef float64
			switch ev := exp.(type) {
			case *PyFloat:
				ef = ev.v
			case *PyInt:
				if n, ok := ev.int64(); ok {
					ef = float64(n)
				}
			}
			return pyFloat(math.Pow(bv.v, ef))
		}
		raiseTypeError("unsupported operand type(s) for pow()")
		return nil
	})
}

func toIntValObj(obj Object) *big.Int {
	switch v := obj.(type) {
	case *PyInt:
		return v.toBigInt()
	case *PyBool:
		if v.v {
			return big.NewInt(1)
		}
		return big.NewInt(0)
	}
	raiseTypeError("expected int, got %s", obj.pyType().Name)
	return nil
}

func makeBuiltinRound() *PyBuiltin {
	return makeBuiltin("round", func(args []Object, kwargs map[string]Object) Object {
		if len(args) < 1 || len(args) > 2 {
			raiseTypeError("round() takes 1 or 2 arguments (%d given)", len(args))
		}
		ndigits := -1
		if len(args) == 2 && args[1] != pyNone {
			ndigits = int(toIntVal(args[1]))
		}
		switch v := args[0].(type) {
		case *PyFloat:
			if ndigits < 0 {
				// Round to int
				return pyInt(int64(math.RoundToEven(v.v)))
			}
			factor := math.Pow10(ndigits)
			return pyFloat(math.RoundToEven(v.v*factor) / factor)
		case *PyInt:
			if ndigits < 0 {
				return v
			}
			return v
		}
		raiseTypeError("type %s doesn't define __round__ method", args[0].pyType().Name)
		return nil
	})
}

func makeBuiltinChr() *PyBuiltin {
	return makeBuiltin("chr", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("chr() takes exactly 1 argument")
		}
		n := toIntVal(args[0])
		if n < 0 || n > 0x10FFFF {
			raiseValueError("chr() arg not in range(0x110000)")
		}
		return pyStr(string(rune(n)))
	})
}

func makeBuiltinOrd() *PyBuiltin {
	return makeBuiltin("ord", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("ord() takes exactly 1 argument")
		}
		switch v := args[0].(type) {
		case *PyStr:
			runes := []rune(v.v)
			if len(runes) != 1 {
				raiseTypeError("ord() expected a character, but string of length %d found", len(runes))
			}
			return pyInt(int64(runes[0]))
		case *PyBytes:
			if len(v.v) != 1 {
				raiseTypeError("ord() expected a character, but bytes of length %d found", len(v.v))
			}
			return pyInt(int64(v.v[0]))
		}
		raiseTypeError("ord() expected string of length 1, but %s found", args[0].pyType().Name)
		return nil
	})
}

func makeBuiltinBin() *PyBuiltin {
	return makeBuiltin("bin", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("bin() takes exactly 1 argument")
		}
		bi := toIntValBig(args[0])
		if bi.Sign() >= 0 {
			return pyStr("0b" + bi.Text(2))
		}
		return pyStr("-0b" + new(big.Int).Neg(bi).Text(2))
	})
}

func makeBuiltinHex() *PyBuiltin {
	return makeBuiltin("hex", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("hex() takes exactly 1 argument")
		}
		bi := toIntValBig(args[0])
		if bi.Sign() >= 0 {
			return pyStr("0x" + bi.Text(16))
		}
		return pyStr("-0x" + new(big.Int).Neg(bi).Text(16))
	})
}

func makeBuiltinOct() *PyBuiltin {
	return makeBuiltin("oct", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("oct() takes exactly 1 argument")
		}
		bi := toIntValBig(args[0])
		if bi.Sign() >= 0 {
			return pyStr("0o" + bi.Text(8))
		}
		return pyStr("-0o" + new(big.Int).Neg(bi).Text(8))
	})
}

func makeBuiltinGetattr() *PyBuiltin {
	return makeBuiltin("getattr", func(args []Object, kwargs map[string]Object) Object {
		if len(args) < 2 || len(args) > 3 {
			raiseTypeError("getattr() takes 2 or 3 arguments")
		}
		obj := args[0]
		name := mustStr(args[1], "getattr")
		val, ok := getAttr(obj, name)
		if !ok {
			if len(args) == 3 {
				return args[2]
			}
			raiseAttributeError(obj.pyType().Name, name)
		}
		return val
	})
}

func makeBuiltinSetattr() *PyBuiltin {
	return makeBuiltin("setattr", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 3 {
			raiseTypeError("setattr() takes exactly 3 arguments")
		}
		obj := args[0]
		name := mustStr(args[1], "setattr")
		val := args[2]
		setAttr(obj, name, val)
		return pyNone
	})
}

func makeBuiltinHasattr() *PyBuiltin {
	return makeBuiltin("hasattr", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 2 {
			raiseTypeError("hasattr() takes exactly 2 arguments")
		}
		obj := args[0]
		name := mustStr(args[1], "hasattr")
		// Try to get the attr; if it panics, return False
		result := func() (found bool) {
			defer func() {
				if r := recover(); r != nil {
					found = false
				}
			}()
			_, found = getAttr(obj, name)
			return found
		}()
		return pyBool(result)
	})
}

func makeBuiltinDelattr() *PyBuiltin {
	return makeBuiltin("delattr", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 2 {
			raiseTypeError("delattr() takes exactly 2 arguments")
		}
		obj := args[0]
		name := mustStr(args[1], "delattr")
		if inst, ok := obj.(*PyInstance); ok {
			delete(inst.Dict, name)
		} else {
			raiseAttributeError(obj.pyType().Name, name)
		}
		return pyNone
	})
}

func makeBuiltinIsinstance() *PyBuiltin {
	return makeBuiltin("isinstance", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 2 {
			raiseTypeError("isinstance() takes exactly 2 arguments")
		}
		obj := args[0]
		classinfo := args[1]
		return pyBool(checkInstance(obj, classinfo))
	})
}

func checkInstance(obj Object, classinfo Object) bool {
	switch cv := classinfo.(type) {
	case *PyClass:
		return isInstance(obj, cv)
	case *PyTuple:
		for _, c := range cv.items {
			if checkInstance(obj, c) {
				return true
			}
		}
		return false
	case *PyType:
		return obj.pyType() == cv ||
			(cv == typeInt && (isIntLike(obj))) ||
			(cv == typeStr && isStrLike(obj)) ||
			(cv == typeBool && isBoolLike(obj))
	case *PyBuiltin:
		// Handle isinstance(x, int/str/float/bool/list/dict/tuple/set/bytes)
		// where the type constructors are PyBuiltin objects.
		switch cv.Name {
		case "int":
			return isIntLike(obj)
		case "str":
			return isStrLike(obj)
		case "float":
			_, ok := obj.(*PyFloat)
			return ok
		case "bool":
			return isBoolLike(obj)
		case "list":
			_, ok := obj.(*PyList)
			return ok
		case "dict":
			_, ok := obj.(*PyDict)
			return ok
		case "tuple":
			_, ok := obj.(*PyTuple)
			return ok
		case "set":
			_, ok := obj.(*PySet)
			return ok
		case "bytes":
			_, ok := obj.(*PyBytes)
			return ok
		}
		return false
	}
	return false
}

func isIntLike(obj Object) bool {
	switch obj.(type) {
	case *PyInt, *PyBool:
		return true
	}
	return false
}

func isStrLike(obj Object) bool {
	_, ok := obj.(*PyStr)
	return ok
}

func isBoolLike(obj Object) bool {
	_, ok := obj.(*PyBool)
	return ok
}

func makeBuiltinIssubclass() *PyBuiltin {
	return makeBuiltin("issubclass", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 2 {
			raiseTypeError("issubclass() takes exactly 2 arguments")
		}
		cls, ok := args[0].(*PyClass)
		if !ok {
			raiseTypeError("issubclass() arg 1 must be a class")
		}
		classinfo := args[1]
		switch cv := classinfo.(type) {
		case *PyClass:
			for _, c := range cls.MRO {
				if c == cv {
					return pyTrue
				}
			}
			return pyFalse
		case *PyTuple:
			for _, c := range cv.items {
				if cls2, ok2 := c.(*PyClass); ok2 {
					for _, mro := range cls.MRO {
						if mro == cls2 {
							return pyTrue
						}
					}
				}
			}
			return pyFalse
		}
		raiseTypeError("issubclass() arg 2 must be a class or tuple of classes")
		return nil
	})
}

func makeBuiltinType() *PyBuiltin {
	return makeBuiltin("type", func(args []Object, kwargs map[string]Object) Object {
		switch len(args) {
		case 1:
			obj := args[0]
			switch v := obj.(type) {
			case *PyInstance:
				return v.Class
			case *PyException:
				return v.ExcClass
			default:
				return obj.pyType()
			}
		case 3:
			name := mustStr(args[0], "type")
			var bases []*PyClass
			if bt, ok := args[1].(*PyTuple); ok {
				for _, b := range bt.items {
					if bc, ok2 := b.(*PyClass); ok2 {
						bases = append(bases, bc)
					}
				}
			}
			dictArg, ok := args[2].(*PyDict)
			if !ok {
				raiseTypeError("type() arg 3 must be a dict")
			}
			cls := &PyClass{
				Name:  name,
				Bases: bases,
				Dict:  make(map[string]Object),
			}
			for i, k := range dictArg.keys {
				if ks, ok2 := k.(*PyStr); ok2 {
					cls.Dict[ks.v] = dictArg.vals[i]
				}
			}
			cls.MRO = computeMRO(cls)
			return cls
		default:
			raiseTypeError("type() takes 1 or 3 arguments (%d given)", len(args))
		}
		return nil
	})
}

func makeBuiltinInt() *PyBuiltin {
	return makeBuiltin("int", func(args []Object, kwargs map[string]Object) Object {
		if len(args) == 0 {
			return pyInt(0)
		}
		obj := args[0]
		base := 10
		if len(args) > 1 {
			base = int(toIntVal(args[1]))
		}
		if v, ok := kwargs["base"]; ok {
			base = int(toIntVal(v))
		}
		switch v := obj.(type) {
		case *PyInt:
			return v
		case *PyBool:
			if v.v {
				return pyInt(1)
			}
			return pyInt(0)
		case *PyFloat:
			return pyInt(int64(v.v))
		case *PyStr:
			s := strings.TrimSpace(v.v)
			// Handle prefix for auto-base detection
			if base == 0 {
				if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
					base = 16
					s = s[2:]
				} else if strings.HasPrefix(s, "0o") || strings.HasPrefix(s, "0O") {
					base = 8
					s = s[2:]
				} else if strings.HasPrefix(s, "0b") || strings.HasPrefix(s, "0B") {
					base = 2
					s = s[2:]
				} else {
					base = 10
				}
			} else if base == 16 && (strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X")) {
				s = s[2:]
			} else if base == 8 && (strings.HasPrefix(s, "0o") || strings.HasPrefix(s, "0O")) {
				s = s[2:]
			} else if base == 2 && (strings.HasPrefix(s, "0b") || strings.HasPrefix(s, "0B")) {
				s = s[2:]
			}
			n, err := strconv.ParseInt(s, base, 64)
			if err != nil {
				// Try big int
				bi := new(big.Int)
				_, ok2 := bi.SetString(s, base)
				if !ok2 {
					raiseValueError("invalid literal for int() with base %d: %s", base, v.pyRepr())
				}
				return pyIntBig(bi)
			}
			return pyInt(n)
		}
		raiseTypeError("int() argument must be a string, a bytes-like object or a number, not '%s'", obj.pyType().Name)
		return nil
	})
}

func makeBuiltinStr() *PyBuiltin {
	return makeBuiltin("str", func(args []Object, kwargs map[string]Object) Object {
		if len(args) == 0 {
			return pyStr("")
		}
		return pyStr(args[0].pyStr())
	})
}

func makeBuiltinFloat() *PyBuiltin {
	return makeBuiltin("float", func(args []Object, kwargs map[string]Object) Object {
		if len(args) == 0 {
			return pyFloat(0)
		}
		obj := args[0]
		switch v := obj.(type) {
		case *PyFloat:
			return v
		case *PyInt:
			if n, ok := v.int64(); ok {
				return pyFloat(float64(n))
			}
			f, _ := new(big.Float).SetInt(v.big).Float64()
			return pyFloat(f)
		case *PyBool:
			if v.v {
				return pyFloat(1)
			}
			return pyFloat(0)
		case *PyStr:
			s := strings.TrimSpace(v.v)
			switch strings.ToLower(s) {
			case "inf", "+inf", "infinity", "+infinity":
				return pyFloat(math.Inf(1))
			case "-inf", "-infinity":
				return pyFloat(math.Inf(-1))
			case "nan":
				return pyFloat(math.NaN())
			}
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				raiseValueError("could not convert string to float: %s", v.pyRepr())
			}
			return pyFloat(f)
		}
		raiseTypeError("float() argument must be a string or a number, not '%s'", obj.pyType().Name)
		return nil
	})
}

func makeBuiltinBool() *PyBuiltin {
	return makeBuiltin("bool", func(args []Object, kwargs map[string]Object) Object {
		if len(args) == 0 {
			return pyFalse
		}
		return pyBool(pyTruth(args[0]))
	})
}

func makeBuiltinList() *PyBuiltin {
	return makeBuiltin("list", func(args []Object, kwargs map[string]Object) Object {
		if len(args) == 0 {
			return pyList(nil)
		}
		items := collectIterable(args[0])
		return pyList(items)
	})
}

func makeBuiltinDict() *PyBuiltin {
	return makeBuiltin("dict", func(args []Object, kwargs map[string]Object) Object {
		d := pyDict()
		if len(args) > 0 {
			switch v := args[0].(type) {
			case *PyDict:
				for i, k := range v.keys {
					d.set(k, v.vals[i])
				}
			default:
				// Assume iterable of (key, value) pairs
				items := collectIterable(args[0])
				for _, item := range items {
					pair, ok := item.(*PyTuple)
					if !ok || len(pair.items) != 2 {
						raiseValueError("dictionary update sequence element is not a 2-tuple")
					}
					d.set(pair.items[0], pair.items[1])
				}
			}
		}
		for k, v := range kwargs {
			d.set(pyStr(k), v)
		}
		return d
	})
}

func makeBuiltinTuple() *PyBuiltin {
	return makeBuiltin("tuple", func(args []Object, kwargs map[string]Object) Object {
		if len(args) == 0 {
			return pyTuple(nil)
		}
		items := collectIterable(args[0])
		return pyTuple(items)
	})
}

func makeBuiltinSet() *PyBuiltin {
	return makeBuiltin("set", func(args []Object, kwargs map[string]Object) Object {
		if len(args) == 0 {
			s := &PySet{items: make(map[any]Object)}
			return s
		}
		items := collectIterable(args[0])
		s, err := pySet(items)
		if err != nil {
			panic(exceptionSignal{exc: newExceptionf(ExcTypeError, "%v", err)})
		}
		return s
	})
}

func makeBuiltinFrozenset() *PyBuiltin {
	return makeBuiltin("frozenset", func(args []Object, kwargs map[string]Object) Object {
		s := &PyFrozenSet{items: make(map[any]Object)}
		if len(args) == 0 {
			return s
		}
		items := collectIterable(args[0])
		for _, item := range items {
			k, err := hashKey(item)
			if err != nil {
				panic(exceptionSignal{exc: newExceptionf(ExcTypeError, "unhashable type: '%s'", item.pyType().Name)})
			}
			s.items[k] = item
		}
		return s
	})
}

func makeBuiltinRepr() *PyBuiltin {
	return makeBuiltin("repr", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("repr() takes exactly 1 argument")
		}
		return pyStr(args[0].pyRepr())
	})
}

func makeBuiltinHash() *PyBuiltin {
	return makeBuiltin("hash", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("hash() takes exactly 1 argument")
		}
		k, err := hashKey(args[0])
		if err != nil {
			panic(exceptionSignal{exc: newExceptionf(ExcTypeError, "unhashable type: '%s'", args[0].pyType().Name)})
		}
		// Convert to a stable int
		switch v := k.(type) {
		case int64:
			return pyInt(v)
		case float64:
			return pyInt(int64(v))
		case string:
			// simple hash
			h := int64(0)
			for _, c := range []byte(v) {
				h = h*31 + int64(c)
			}
			return pyInt(h)
		case nil:
			return pyInt(0)
		}
		return pyInt(0)
	})
}

func makeBuiltinId() *PyBuiltin {
	return makeBuiltin("id", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("id() takes exactly 1 argument")
		}
		// Return a stable per-object identifier. Using the pointer stored
		// in the interface value (args[0]) rather than &args[0] (the slice
		// slot address) ensures the same object always returns the same id.
		id := fmt.Sprintf("%p", args[0])
		// Parse hex pointer address
		if len(id) > 2 {
			n, err := strconv.ParseInt(id[2:], 16, 64)
			if err == nil {
				return pyInt(n)
			}
		}
		return pyInt(0)
	})
}

func makeBuiltinCallable() *PyBuiltin {
	return makeBuiltin("callable", func(args []Object, kwargs map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("callable() takes exactly 1 argument")
		}
		return pyBool(isCallable(args[0]))
	})
}

func isCallable(obj Object) bool {
	switch obj.(type) {
	case *PyFunction, *PyBuiltin, *PyBoundMethod, *PyClass:
		return true
	case *PyInstance:
		inst := obj.(*PyInstance)
		_, ok := inst.lookupMethod("__call__")
		return ok
	}
	return false
}

func makeBuiltinNext() *PyBuiltin {
	return makeBuiltin("next", func(args []Object, kwargs map[string]Object) Object {
		if len(args) < 1 || len(args) > 2 {
			raiseTypeError("next() takes 1 or 2 arguments (%d given)", len(args))
		}
		val, ok := nextFromIterable(args[0])
		if !ok {
			if len(args) == 2 {
				return args[1]
			}
			panic(exceptionSignal{exc: newException(ExcStopIteration)})
		}
		return val
	})
}

func makeBuiltinIter() *PyBuiltin {
	return makeBuiltin("iter", func(args []Object, kwargs map[string]Object) Object {
		if len(args) < 1 {
			raiseTypeError("iter() requires at least 1 argument")
		}
		obj := args[0]
		// Return an appropriate iterator
		switch v := obj.(type) {
		case *PyList:
			return &PyListIter{items: v.items}
		case *PyTuple:
			return &PyListIter{items: v.items}
		case *PyStr:
			runes := []rune(v.v)
			items := make([]Object, len(runes))
			for i, r := range runes {
				items[i] = pyStr(string(r))
			}
			return &PyListIter{items: items}
		case *PyRange:
			return &rangeIter{r: v, cur: v.start}
		case *PyDict:
			keys := make([]Object, len(v.keys))
			copy(keys, v.keys)
			return &PyDictKeyIter{keys: keys}
		case *PySet:
			items := make([]Object, 0, len(v.items))
			for _, item := range v.items {
				items = append(items, item)
			}
			return &PyListIter{items: items}
		case *rangeIter, *PyMapIter, *PyFilterIter, *PyZipIter, *PyEnumerateIter, *PyReversedIter, *PyListIter, *PyDictKeyIter, *PyGenerator:
			return obj
		case *PyInstance:
			if fn, ok2 := v.lookupMethod("__iter__"); ok2 {
				return callObject(fn, []Object{v}, nil)
			}
		}
		raiseTypeError("'%s' object is not iterable", obj.pyType().Name)
		return nil
	})
}

func makeBuiltinInput(opts *RunOpts) *PyBuiltin {
	return makeBuiltin("input", func(args []Object, kwargs map[string]Object) Object {
		if len(args) > 0 {
			fmt.Fprint(opts.Stdout, args[0].pyStr())
		}
		if opts.Stdin == nil || opts.stdinReader == nil {
			return pyStr("")
		}
		// opts.stdinReader is a single persistent bufio.Reader (initialised by
		// runInternal) so read-ahead bytes are not dropped between input() calls.
		// The underlying reader is already wrapped in a global LimitReader.
		line, err := opts.stdinReader.ReadString('\n')
		if err != nil && err != io.EOF {
			panic(exceptionSignal{exc: newExceptionf(ExcOSError, "input error: %v", err)})
		}
		line = strings.TrimRight(line, "\n")
		line = strings.TrimRight(line, "\r")
		return pyStr(line)
	})
}

func makeBuiltinVars() *PyBuiltin {
	return makeBuiltin("vars", func(args []Object, kwargs map[string]Object) Object {
		if len(args) == 0 {
			// Return current locals — need scope context; return empty dict for now
			return pyDict()
		}
		obj := args[0]
		switch v := obj.(type) {
		case *PyInstance:
			d := pyDict()
			for k, val := range v.Dict {
				d.set(pyStr(k), val)
			}
			return d
		case *PyModule:
			d := pyDict()
			for k, val := range v.Dict {
				d.set(pyStr(k), val)
			}
			return d
		case *PyClass:
			d := pyDict()
			for k, val := range v.Dict {
				d.set(pyStr(k), val)
			}
			return d
		}
		raiseTypeError("vars() argument must have __dict__ attribute")
		return nil
	})
}

func makeBuiltinDir() *PyBuiltin {
	return makeBuiltin("dir", func(args []Object, kwargs map[string]Object) Object {
		var names []string
		if len(args) > 0 {
			obj := args[0]
			switch v := obj.(type) {
			case *PyInstance:
				for k := range v.Dict {
					names = append(names, k)
				}
				for _, cls := range v.Class.MRO {
					for k := range cls.Dict {
						names = append(names, k)
					}
				}
			case *PyModule:
				for k := range v.Dict {
					names = append(names, k)
				}
			case *PyClass:
				for k := range v.Dict {
					names = append(names, k)
				}
			}
		}
		// Deduplicate and sort
		seen := make(map[string]bool)
		result := make([]Object, 0)
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				result = append(result, pyStr(n))
			}
		}
		sortList(result, nil, false)
		return pyList(result)
	})
}

func makeBuiltinFormat() *PyBuiltin {
	return makeBuiltin("format", func(args []Object, kwargs map[string]Object) Object {
		if len(args) < 1 {
			raiseTypeError("format() requires at least 1 argument")
		}
		val := args[0]
		spec := ""
		if len(args) > 1 {
			spec = mustStr(args[1], "format")
		}
		s := val.pyStr()
		if spec != "" {
			s = applyFormatSpec(s, val, spec)
		}
		return pyStr(s)
	})
}

func makeBuiltinBytes() *PyBuiltin {
	return makeBuiltin("bytes", func(args []Object, kwargs map[string]Object) Object {
		if len(args) == 0 {
			return pyBytes([]byte{})
		}
		switch v := args[0].(type) {
		case *PyInt:
			n, _ := v.int64()
			if n < 0 {
				raiseValueError("bytes length must be >= 0")
			}
			if n > maxRepeatBytes {
				panic(exceptionSignal{exc: newExceptionf(ExcMemoryError, "bytes() size %d exceeds limit (%d)", n, maxRepeatBytes)})
			}
			return pyBytes(make([]byte, n))
		case *PyStr:
			// Requires encoding
			enc := "utf-8"
			if len(args) > 1 {
				enc = strings.ToLower(mustStr(args[1], "bytes"))
			}
			_ = enc // Only support UTF-8
			return pyBytes([]byte(v.v))
		case *PyBytes:
			cp := make([]byte, len(v.v))
			copy(cp, v.v)
			return pyBytes(cp)
		default:
			// Try iterable of ints
			items := collectIterable(args[0])
			b := make([]byte, len(items))
			for i, item := range items {
				n := toIntVal(item)
				if n < 0 || n > 255 {
					raiseValueError("bytes must be in range(0, 256)")
				}
				b[i] = byte(n)
			}
			return pyBytes(b)
		}
	})
}

func makeBuiltinBytearray() *PyBuiltin {
	return makeBuiltin("bytearray", func(args []Object, kwargs map[string]Object) Object {
		// Return a mutable bytes-like — for simplicity return PyBytes
		if len(args) == 0 {
			return pyBytes([]byte{})
		}
		switch v := args[0].(type) {
		case *PyInt:
			n, _ := v.int64()
			if n < 0 {
				raiseValueError("bytearray() length must be >= 0")
			}
			if n > maxRepeatBytes {
				panic(exceptionSignal{exc: newExceptionf(ExcMemoryError, "bytearray() size %d exceeds limit (%d)", n, maxRepeatBytes)})
			}
			return pyBytes(make([]byte, n))
		case *PyStr:
			return pyBytes([]byte(v.v))
		case *PyBytes:
			cp := make([]byte, len(v.v))
			copy(cp, v.v)
			return pyBytes(cp)
		default:
			items := collectIterable(args[0])
			b := make([]byte, len(items))
			for i, item := range items {
				b[i] = byte(toIntVal(item))
			}
			return pyBytes(b)
		}
	})
}

func makeBuiltinMemoryview() *PyBuiltin {
	return makeBuiltin("memoryview", func(args []Object, kwargs map[string]Object) Object {
		raiseTypeError("memoryview() is not supported in this shell")
		return nil
	})
}

func makeBuiltinOpen(opts *RunOpts) *PyBuiltin {
	return makeBuiltin("open", func(args []Object, kwargs map[string]Object) Object {
		if len(args) < 1 {
			raiseTypeError("open() requires at least 1 argument")
		}
		var path string
		switch v := args[0].(type) {
		case *PyStr:
			path = v.v
		case *PyBytes:
			path = string(v.v)
		default:
			raiseTypeError("open() argument 1 must be str, not %s", args[0].pyType().Name)
		}

		mode := "r"
		if len(args) > 1 {
			mode = mustStr(args[1], "open")
		}
		if v, ok := kwargs["mode"]; ok {
			mode = mustStr(v, "open")
		}

		// Reject write/append/exclusive modes
		for _, ch := range mode {
			switch ch {
			case 'w', 'a', 'x', '+':
				panic(exceptionSignal{exc: newExceptionf(ExcPermissionError, "open() in write mode is not permitted in this shell")})
			}
		}

		binary := strings.ContainsRune(mode, 'b')

		rc, err := opts.Open(opts.Ctx, path, os.O_RDONLY, 0)
		if err != nil {
			if os.IsNotExist(err) {
				panic(exceptionSignal{exc: newExceptionf(ExcFileNotFoundError, "[Errno 2] No such file or directory: '%s'", path)})
			}
			panic(exceptionSignal{exc: newExceptionf(ExcOSError, "cannot open %q: %v", path, err)})
		}

		return &PyFile{rc: rc, name: path, binary: binary}
	})
}

func makeBuiltinSuper() *PyBuiltin {
	return makeBuiltin("super", func(args []Object, kwargs map[string]Object) Object {
		// Return a sentinel; eval.go must intercept super() calls inside methods
		return &PySuper{}
	})
}

// PySuper is the sentinel returned by super().
type PySuper struct {
	Class *PyClass
	Obj   Object
}

func (s *PySuper) pyType() *PyType { return typeClass }
func (s *PySuper) pyRepr() string  { return "<super object>" }
func (s *PySuper) pyStr() string   { return s.pyRepr() }

func makeBuiltinObject() *PyBuiltin {
	return makeBuiltin("object", func(args []Object, kwargs map[string]Object) Object {
		cls := &PyClass{
			Name: "object",
			Dict: make(map[string]Object),
		}
		cls.MRO = []*PyClass{cls}
		return &PyInstance{Class: cls, Dict: make(map[string]Object)}
	})
}

// getAttr retrieves an attribute from an object.
func getAttr(obj Object, name string) (Object, bool) {
	switch v := obj.(type) {
	case *PyInstance:
		// Check instance dict first
		if val, ok := v.Dict[name]; ok {
			return val, true
		}
		// Then class MRO
		for _, cls := range v.Class.MRO {
			if val, ok2 := cls.Dict[name]; ok2 {
				// Bind if it's a function
				if fn, ok3 := val.(*PyFunction); ok3 {
					return &PyBoundMethod{Self: v, Func: fn}, true
				}
				return val, true
			}
		}
		return nil, false
	case *PyModule:
		if val, ok := v.Dict[name]; ok {
			return val, true
		}
		return nil, false
	case *PyClass:
		if val, ok := v.Dict[name]; ok {
			return val, true
		}
		// Check base classes
		for _, base := range v.MRO[1:] {
			if val, ok2 := base.Dict[name]; ok2 {
				return val, true
			}
		}
		return nil, false
	case *PyStr:
		return strGetAttr(v, name)
	case *PyList:
		return listGetAttr(v, name)
	case *PyDict:
		return dictGetAttr(v, name)
	case *PySet:
		return setGetAttr(v, name)
	case *PyBytes:
		return bytesGetAttr(v, name)
	case *PyFile:
		return fileGetAttr(v, name)
	case *PyException:
		// Check dict first
		if v.Dict != nil {
			if val, ok := v.Dict[name]; ok {
				return val, true
			}
		}
		// Common attributes
		switch name {
		case "args":
			return pyTuple(v.Args), true
		case "__class__":
			return v.ExcClass, true
		case "__cause__":
			if v.Cause != nil {
				return v.Cause, true
			}
			return pyNone, true
		case "__context__":
			if v.Context != nil {
				return v.Context, true
			}
			return pyNone, true
		}
		return nil, false
	case *PySuper:
		if v.Obj != nil {
			// Look up in parent classes
			if inst, ok2 := v.Obj.(*PyInstance); ok2 {
				// Skip the first class (current)
				for i, cls := range inst.Class.MRO {
					if i == 0 {
						continue
					}
					if val, ok3 := cls.Dict[name]; ok3 {
						if fn, ok4 := val.(*PyFunction); ok4 {
							return &PyBoundMethod{Self: inst, Func: fn}, true
						}
						return val, true
					}
					_ = cls
				}
			}
		}
		return nil, false
	case *PyGenerator:
		switch name {
		case "send":
			return makeBuiltin("send", func(args []Object, kwargs map[string]Object) Object {
				if len(args) != 1 {
					raiseTypeError("send() takes exactly one argument")
				}
				if v.done {
					panic(exceptionSignal{exc: newException(ExcStopIteration, nil)})
				}
				if !v.awaitingSend {
					raiseTypeError("can't send non-None value to a just-started generator")
				}
				// Send value into generator (unblock its sendCh receive).
				v.sendCh <- args[0]
				v.awaitingSend = false
				// Receive the next yielded value.
				val, ok := <-v.yieldCh
				if !ok {
					v.done = true
					panic(exceptionSignal{exc: newException(ExcStopIteration, nil)})
				}
				v.awaitingSend = true
				return val
			}), true
		case "__next__":
			return makeBuiltin("__next__", func(args []Object, kwargs map[string]Object) Object {
				if v.done {
					panic(exceptionSignal{exc: newException(ExcStopIteration, nil)})
				}
				if v.awaitingSend {
					v.sendCh <- pyNone
					v.awaitingSend = false
				}
				val, ok := <-v.yieldCh
				if !ok {
					v.done = true
					panic(exceptionSignal{exc: newException(ExcStopIteration, nil)})
				}
				v.awaitingSend = true
				return val
			}), true
		case "close":
			return makeBuiltin("close", func(args []Object, kwargs map[string]Object) Object {
				v.done = true
				return pyNone
			}), true
		case "__iter__":
			return makeBuiltin("__iter__", func(args []Object, kwargs map[string]Object) Object {
				return v
			}), true
		}
		return nil, false
	case *PyTuple:
		switch name {
		case "count":
			return makeBuiltin("count", func(args []Object, kwargs map[string]Object) Object {
				if len(args) != 1 {
					raiseTypeError("count() takes exactly 1 argument")
				}
				n := 0
				for _, item := range v.items {
					if pyEq(item, args[0]) {
						n++
					}
				}
				return pyInt(int64(n))
			}), true
		case "index":
			return makeBuiltin("index", func(args []Object, kwargs map[string]Object) Object {
				if len(args) < 1 {
					raiseTypeError("index() requires at least 1 argument")
				}
				for i, item := range v.items {
					if pyEq(item, args[0]) {
						return pyInt(int64(i))
					}
				}
				raiseValueError("tuple.index(x): x not in tuple")
				return nil
			}), true
		}
		return nil, false
	}
	return nil, false
}

// setAttr sets an attribute on an object.
func setAttr(obj Object, name string, val Object) {
	switch v := obj.(type) {
	case *PyInstance:
		v.Dict[name] = val
	case *PyModule:
		v.Dict[name] = val
	case *PyClass:
		v.Dict[name] = val
	case *PyException:
		if v.Dict == nil {
			v.Dict = make(map[string]Object)
		}
		v.Dict[name] = val
	default:
		raiseAttributeError(obj.pyType().Name, name)
	}
}

// pyAdd adds two Python objects.
func pyAdd(a, b Object) Object {
	switch av := a.(type) {
	case *PyInt:
		switch bv := b.(type) {
		case *PyInt:
			an := av.toBigInt()
			bn := bv.toBigInt()
			result := new(big.Int).Add(an, bn)
			return pyIntBig(result)
		case *PyFloat:
			if n, ok := av.int64(); ok {
				return pyFloat(float64(n) + bv.v)
			}
		case *PyBool:
			var bi int64
			if bv.v {
				bi = 1
			}
			result := new(big.Int).Add(av.toBigInt(), big.NewInt(bi))
			return pyIntBig(result)
		}
	case *PyFloat:
		switch bv := b.(type) {
		case *PyFloat:
			return pyFloat(av.v + bv.v)
		case *PyInt:
			if n, ok := bv.int64(); ok {
				return pyFloat(av.v + float64(n))
			}
		}
	case *PyBool:
		var ai int64
		if av.v {
			ai = 1
		}
		switch bv := b.(type) {
		case *PyBool:
			var bi int64
			if bv.v {
				bi = 1
			}
			return pyInt(ai + bi)
		case *PyInt:
			result := new(big.Int).Add(big.NewInt(ai), bv.toBigInt())
			return pyIntBig(result)
		}
	case *PyStr:
		if bv, ok := b.(*PyStr); ok {
			return pyStr(av.v + bv.v)
		}
	case *PyList:
		if bv, ok := b.(*PyList); ok {
			items := make([]Object, len(av.items)+len(bv.items))
			copy(items, av.items)
			copy(items[len(av.items):], bv.items)
			return pyList(items)
		}
	case *PyTuple:
		if bv, ok := b.(*PyTuple); ok {
			items := make([]Object, len(av.items)+len(bv.items))
			copy(items, av.items)
			copy(items[len(av.items):], bv.items)
			return pyTuple(items)
		}
	case *PyBytes:
		if bv, ok := b.(*PyBytes); ok {
			result := make([]byte, len(av.v)+len(bv.v))
			copy(result, av.v)
			copy(result[len(av.v):], bv.v)
			return pyBytes(result)
		}
	}
	raiseTypeError("unsupported operand type(s) for +: '%s' and '%s'", a.pyType().Name, b.pyType().Name)
	return nil
}
