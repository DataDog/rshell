// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

// permanentlyBanned lists packages that may never be imported,
// regardless of what symbols they export.
// Keys ending with "/" are treated as prefix bans (any import path starting
// with that prefix is banned); all other keys are exact-match bans.
var permanentlyBanned = map[string]string{
	"reflect": "reflection defeats static safety analysis",
	"unsafe":  "bypasses Go's type and memory safety guarantees",
	"os/exec": "spawns arbitrary host processes, bypassing all shell restrictions",
	"net":     "raw network access enables data exfiltration and reverse shells",
	"net/":    "network sub-packages enable data exfiltration and C2 communication",
	"plugin":  "dynamically loads Go shared libraries, enabling arbitrary code execution",
}
