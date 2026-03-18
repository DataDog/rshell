// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package allowedsymbols defines the symbol-level import allowlists that CI
// static-analysis checks enforce across three code areas of the shell:
//
//	interp/             → interpAllowedSymbols    (symbols_interp.go)
//	builtins/           → builtinAllowedSymbols   (symbols_builtins.go)
//	builtins/internal/  → internalAllowedSymbols  (symbols_internal.go)
//
// # How the allowlists work
//
// Every Go import "importpath.Symbol" that appears in the listed code areas
// must have a matching entry in the relevant allowlist. The CI check rejects
// any import that is missing from the list, even if the package itself is not
// permanently banned. This creates a two-layer defence:
//
//  1. permanentlyBanned (this file) — packages that can never be imported,
//     regardless of which symbol is used (e.g. reflect, os/exec).
//  2. Per-area allowlists — the explicit set of symbols permitted in each
//     code area. Everything else is implicitly banned.
//
// # Adding a new symbol
//
// To permit a new symbol in a code area:
//  1. Confirm its package is not in permanentlyBanned.
//  2. Add "importpath.Symbol" to the appropriate allowlist
//     (builtinAllowedSymbols, interpAllowedSymbols, or internalAllowedSymbols)
//     with a short inline comment stating what the symbol does and why it is safe.
//  3. If the symbol is for a specific builtin command, also add it to
//     builtinPerCommandSymbols[<command>] in symbols_builtins.go.
//  4. Run `go test ./allowedsymbols/...` to verify the lists are consistent.
//
// # Safety categories (builtinAllowedSymbols)
//
// builtinAllowedSymbols is ordered from least safe to most safe. When
// reviewing a new addition or auditing the list, start from the top:
//
//  1. Network I/O          — symbols that initiate real network connections
//     (highest risk: data exfiltration, C2 communication).
//  2. OS/Kernel interface  — syscalls, kernel table reads, DLL calls.
//  3. OS network state     — read-only enumeration of local network interfaces.
//  4. Filesystem metadata  — read-only file/directory metadata; no file contents.
//  5. Regular expressions  — RE2-backed (linear time), but can be CPU-intensive.
//  6. Context & time       — deadline management and wall-clock reads; no I/O.
//  7. I/O interfaces       — Reader/Writer/Closer interfaces and stream utilities;
//     no direct filesystem or network access by themselves.
//  8. String manipulation  — pure in-memory string and string-conversion functions.
//  9. Math & collections   — pure arithmetic and slice operations.
//
// 10. Unicode              — pure Unicode data tables and classification functions.
// 11. Error handling       — pure error construction and formatting.
package allowedsymbols

// permanentlyBanned lists packages that may never be imported,
// regardless of what symbols they export.
// Keys ending with "/" are treated as prefix bans (any import path starting
// with that prefix is banned); all other keys are exact-match bans.
var permanentlyBanned = map[string]string{
	"reflect": "reflection defeats static safety analysis",
	"os/exec": "spawns arbitrary host processes, bypassing all shell restrictions",
	// NOTE: "net" (the base package) is intentionally NOT banned here so that the
	// ip builtin can use read-only interface enumeration (net.Interfaces, net.Interface,
	// net.IPNet, etc.) via the symbol-level allowlist. Only the safe, non-networking
	// symbols are permitted; connection-oriented symbols (net.Dial, net.Listen, etc.)
	// are not in builtinAllowedSymbols and therefore cannot be used. All net/ sub-packages
	// (net/http, net/smtp, etc.) remain permanently banned below.
	"net/":   "network sub-packages enable data exfiltration and C2 communication",
	"plugin": "dynamically loads Go shared libraries, enabling arbitrary code execution",
}
