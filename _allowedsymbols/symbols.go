// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

// permanentlyBanned lists packages that may never be imported by builtin
// command implementations, regardless of what symbols they export.
var permanentlyBanned = map[string]string{
	"reflect": "reflection defeats static safety analysis",
	"unsafe":  "bypasses Go's type and memory safety guarantees",
}
