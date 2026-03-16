// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package restrictedtime provides time-comparison helpers for the shell interpreter.
// Builtins never import this package directly — they receive boolean-returning
// closures via CallContext, so they cannot obtain raw time values.
package restrictedtime

import (
	"math"
	"time"
)

// Comparison operators matching find's cmpOp values.
const (
	CmpLess  = -1
	CmpExact = 0
	CmpMore  = 1
)

// MaxMtimeN is the largest N for which (N+1)*24*time.Hour does not overflow.
const MaxMtimeN = int64(math.MaxInt64/(int64(24*time.Hour))) - 1

// MaxMminN is the largest N for which time.Duration(N)*time.Minute
// does not overflow int64 nanoseconds.
const MaxMminN = int64(math.MaxInt64 / int64(time.Minute))

// MatchMtime checks modification time in days using GNU find semantics.
//
// Comparison modes:
//   - CmpExact (N): day-bucketed — floor(delta_hours/24) == N.
//   - CmpMore (+N): raw seconds — delta >= (N+1)*86400.
//   - CmpLess (-N): raw seconds — delta < N*86400.
//
// now is truncated to second precision for +N/-N to match GNU find's time().
func MatchMtime(now, modTime time.Time, n int64, cmp int) bool {
	switch cmp {
	case CmpMore: // +N: strictly older than (N+1) days
		if n > MaxMtimeN {
			return false // threshold beyond representable duration
		}
		diff := now.Truncate(time.Second).Sub(modTime)
		return diff >= time.Duration(n+1)*24*time.Hour
	case CmpLess: // -N: strictly newer than N days
		if n > MaxMtimeN {
			return true // threshold beyond representable duration
		}
		diff := now.Truncate(time.Second).Sub(modTime)
		return diff < time.Duration(n)*24*time.Hour
	default: // N: day-bucketed exact match
		diff := now.Sub(modTime)
		days := int64(math.Floor(diff.Hours() / 24))
		return days == n
	}
}

// MatchMmin checks modification time in minutes using GNU find semantics.
//
// Comparison modes:
//   - CmpExact (N): ceiling-bucketed — ceil(delta_seconds/60) == N.
//   - CmpMore (+N): raw seconds — delta > N*60.
//   - CmpLess (-N): raw seconds — delta < N*60.
func MatchMmin(now, modTime time.Time, n int64, cmp int) bool {
	diff := now.Sub(modTime)
	switch cmp {
	case CmpMore: // +N: strictly older than N minutes
		if n > MaxMminN {
			return false // threshold beyond representable duration; nothing qualifies
		}
		return diff > time.Duration(n)*time.Minute
	case CmpLess: // -N: strictly newer than N minutes
		if n > MaxMminN {
			return true // threshold beyond representable duration; everything qualifies
		}
		return diff < time.Duration(n)*time.Minute
	default: // N: ceiling-bucketed exact match
		mins := int64(math.Ceil(diff.Minutes()))
		return mins == n
	}
}

// IsRecentEnough reports whether modTime is within the given number of months
// before now and not in the future. Used by ls to decide between showing
// HH:MM (recent) or year (old/future) in long listing format.
func IsRecentEnough(now, modTime time.Time, months int) bool {
	cutoff := now.AddDate(0, -months, 0)
	return !modTime.Before(cutoff) && !modTime.After(now)
}

// NewCallbacks returns MatchMtime, MatchMmin, and IsRecentEnough closures
// that capture a single invocation timestamp internally via time.Now().
// This allows the caller to wire time-comparison callbacks without importing
// the time package directly.
func NewCallbacks() (
	matchMtime func(time.Time, int64, int) bool,
	matchMmin func(time.Time, int64, int) bool,
	isRecentEnough func(time.Time, int) bool,
) {
	now := time.Now()
	return func(modTime time.Time, n int64, cmp int) bool {
			return MatchMtime(now, modTime, n, cmp)
		},
		func(modTime time.Time, n int64, cmp int) bool {
			return MatchMmin(now, modTime, n, cmp)
		},
		func(modTime time.Time, months int) bool {
			return IsRecentEnough(now, modTime, months)
		}
}
