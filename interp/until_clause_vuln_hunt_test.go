// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Campaign: 2026-05-20-gpt-5.5-cyber-3

func TestVulnHuntShellFeatureReadonlyBypass_UntilConditionReadonlyAssignmentDoesNotMutate(t *testing.T) {
	stdout, stderr := runScriptWithReadonly(t, `until RO_VAR=hacked false; do
  echo body=$RO_VAR
  break
done
echo after=$RO_VAR
`)

	assert.Contains(t, stderr, "readonly variable")
	assert.NotContains(t, stdout, "hacked")
	assert.Contains(t, stdout, "after=original")
}

func TestVulnHuntShellFeatureReadonlyBypass_UntilBodyReadonlyAssignmentDoesNotMutate(t *testing.T) {
	stdout, stderr := runScriptWithReadonly(t, `until false; do
  RO_VAR=hacked echo body=$RO_VAR
  break
done
echo after=$RO_VAR
`)

	assert.Contains(t, stderr, "readonly variable")
	assert.NotContains(t, stdout, "hacked")
	assert.Contains(t, stdout, "after=original")
}
