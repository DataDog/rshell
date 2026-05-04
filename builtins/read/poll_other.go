// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !unix

package read

// pollInputNonConsuming returns supported=false on platforms where
// rshell does not implement a non-blocking poll. The caller falls back
// to a consume-based probe (read one byte with a deadline in the past)
// — that fallback is best-effort and consumes one byte on success,
// diverging from bash's non-consuming semantics. Implementing
// non-consuming poll on Windows is left for a follow-up.
func pollInputNonConsuming(fd uintptr) (available, supported bool) {
	return false, false
}
