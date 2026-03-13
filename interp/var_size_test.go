// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/interp"
)

// TestOversizedInlineVarAbortsCommand verifies that an inline assignment whose
// value exceeds MaxVarBytes does NOT execute the following command and that the
// shell exits with a non-zero status.
func TestOversizedInlineVarAbortsCommand(t *testing.T) {
	large := strings.Repeat("x", interp.MaxVarBytes+1)
	script := fmt.Sprintf("X=%s echo SHOULD_NOT_RUN", large)

	stdout, stderr, code := runScript(t, script, "")

	assert.NotContains(t, stdout, "SHOULD_NOT_RUN", "command must not execute after oversized inline assignment")
	assert.Contains(t, stderr, "value too large")
	assert.NotEqual(t, 0, code, "exit code must be non-zero")
}
