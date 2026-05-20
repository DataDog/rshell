// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt 2026-05-20-gpt-5.5-cyber-3 (target: simple_command)

package interp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVulnHuntShellFeatureReadonlyBypass_FailedInlineReadonlyDoesNotMutateViaLaterSubst(t *testing.T) {
	stdout, stderr := runScriptWithReadonly(t,
		"RO_VAR=evil FOO=$(echo SIDE_EFFECT >&2) echo HIT\necho after foo=$FOO ro=$RO_VAR\n")

	assert.Contains(t, stderr, "readonly variable",
		"readonly inline assignment must fail visibly")
	assert.Contains(t, stderr, "SIDE_EFFECT",
		"later assignment-value command substitutions still run during expansion")
	assert.NotContains(t, stdout, "HIT",
		"the command body must not execute after a readonly inline assignment failure")
	assert.Contains(t, stdout, "after foo= ro=original",
		"sibling inline assignments must be restored and readonly value must remain unchanged")
}
