// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMapAwkCasePreservesInvalidUTF8(t *testing.T) {
	require.Equal(t, "é\xfea\xff�", mapAwkCase("É\xfeA\xff�", strings.ToLower))
	require.Equal(t, "É\xfeA\xff�", mapAwkCase("é\xfea\xff�", strings.ToUpper))
}
