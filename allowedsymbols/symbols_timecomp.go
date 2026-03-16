// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

// timecompAllowedSymbols lists every "importpath.Symbol" that may be used by
// non-test Go files in timecomp/. Each entry must be in "importpath.Symbol"
// form, where importpath is the full Go import path.
//
// Each symbol must have a comment explaining what it does and why it is safe
// to use inside the time-comparison package.
//
// Internal module imports (github.com/DataDog/rshell/*) are auto-allowed
// and do not appear here.
//
// The permanently banned packages (reflect, unsafe) apply here too.
var timecompAllowedSymbols = []string{
	"math.Ceil",     // pure arithmetic; rounds up fractional minutes for ceiling bucketing.
	"math.Floor",    // pure arithmetic; rounds down fractional days for floor bucketing.
	"math.MaxInt64", // integer constant; used for overflow guards.
	"time.Duration", // duration type; pure integer alias, no I/O.
	"time.Hour",     // constant representing one hour; no side effects.
	"time.Minute",   // constant representing one minute; no side effects.
	"time.Second",   // constant representing one second; no side effects.
	"time.Time",     // time value type; pure data, no side effects.
}
