// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Vulnerability-hunt regression tests for campaign 2026-05-20-gpt-5.5-cyber-3.
// These tests pin blocked attack paths only. Working exploit PoCs remain in the
// private vuln-hunt repository until a fix ships.

package ping_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestVulnHuntBuiltinIntegerOverflow_CountParseOverflowRejected(t *testing.T) {
	stdout, stderr, code := cmdRun(t, "ping -c 999999999999999999999999999999 127.0.0.1")
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "invalid argument")
}

func TestVulnHuntBuiltinIntegerOverflow_InvalidDurationsWithoutHelpRejected(t *testing.T) {
	for _, script := range []string{
		"ping -W NaN 127.0.0.1",
		"ping -i +Inf 127.0.0.1",
		"ping -W 1e20 127.0.0.1",
		"ping -i -1s 127.0.0.1",
	} {
		t.Run(script, func(t *testing.T) {
			stdout, stderr, code := cmdRun(t, script)
			assert.Equal(t, 1, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "invalid argument")
		})
	}
}

func TestVulnHuntBuiltinResourceExhaustion_LongHostnameContextBounded(t *testing.T) {
	host := strings.Repeat("a", 10000) + ".invalid"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, code := runScriptCtx(ctx, t, "ping -c 1 -- "+host)
	assert.Equal(t, 1, code)
	assert.NoError(t, ctx.Err(), "long hostname must fail before the context deadline")
}
