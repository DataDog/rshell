// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tests_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

// Campaign: 2026-05-20-gpt-5.5-cyber-3

func TestVulnHuntShellFeatureExpansionChain_UntilConditionDoesNotReparseExpandedSyntax(t *testing.T) {
	stdout, stderr, code := whileRun(t, `PAYLOAD='false; echo HACKED'
until $PAYLOAD; do
  echo body
  break
done
echo done
`)

	assert.Equal(t, 0, code)
	assert.Equal(t, "body\ndone\n", stdout)
	assert.NotContains(t, stdout, "HACKED")
	assert.Contains(t, stderr, "false;")
}

func TestVulnHuntShellFeatureExpansionChain_UntilGlobStaysSandboxed(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	secret := filepath.Join(root, "secret")
	assert.NoError(t, os.Mkdir(allowed, 0755))
	assert.NoError(t, os.Mkdir(secret, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(secret, "hidden.txt"), []byte("secret"), 0644))

	stdout, stderr, code := testutil.RunScript(t, `PAT='../secret/*'
until echo $PAT; do
  echo body
  break
done
`, allowed, interp.AllowedPaths([]string{allowed}))

	assert.Equal(t, 0, code)
	assert.Equal(t, "../secret/*\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureParserConfusion_UntilConditionStatusCompositions(t *testing.T) {
	stdout, stderr, code := whileRun(t, `i=
until ! [ "$i" != aa ]; do
  i="${i}a"
  echo "neg:$i"
done
until echo test | grep -q match; do
  echo pipe-body
  break
done
until true && false; do
  echo and-body
  break
done
`)

	assert.Equal(t, 0, code)
	assert.Equal(t, "neg:a\nneg:aa\npipe-body\nand-body\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureRedirectionChain_UntilInputRedirectOutsideAllowedBlocked(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	secret := filepath.Join(root, "secret")
	assert.NoError(t, os.Mkdir(allowed, 0755))
	assert.NoError(t, os.Mkdir(secret, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(secret, "hidden.txt"), []byte("secret\n"), 0644))

	stdout, stderr, code := testutil.RunScript(t, `until false; do
  cat
  break
done < ../secret/hidden.txt
`, allowed, interp.AllowedPaths([]string{allowed}))

	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.NotContains(t, stdout, "secret")
	assert.Contains(t, stderr, "permission denied")
	assert.NotContains(t, stderr, "secret\n")
}

func TestVulnHuntShellFeatureSignalContext_UntilConditionPipelineCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, _ = whileRunCtx(ctx, t, `until echo x | grep -q y; do :; done`)
	assert.Less(t, time.Since(start), 5*time.Second, "until condition pipeline ignored cancellation")
}

func TestVulnHuntShellFeatureSignalContext_SubshellInfiniteUntilRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, _ = whileRunCtx(ctx, t, `( until false; do :; done )`)
	assert.Less(t, time.Since(start), 5*time.Second, "subshell-wrapped until ignored cancellation")
}

func TestVulnHuntShellFeatureSignalContext_UntilLargeHeredocRedirectCancellation(t *testing.T) {
	body := strings.Repeat("x", 256*1024)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, _ = whileRunCtx(ctx, t, "until false; do cat <<'EOF' >/dev/null\n"+body+"\nEOF\ndone")
	assert.Less(t, time.Since(start), 5*time.Second, "until heredoc writer ignored cancellation")
}
