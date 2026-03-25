// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tests_test

// Pipeline resource-usage tests.
//
// These tests document two resource-usage gaps in the pipeline implementation:
//
//  1. Sort-chain memory: each sort in a pipeline buffers its entire input
//     (up to MaxTotalBytes = 256 MiB) in allLines before emitting output.
//     Because the left side of every pipe runs concurrently with the right
//     side, N sorts in a chain hold their buffers simultaneously at peak —
//     producing N × 256 MiB of live allocations with no aggregate cap.
//
//  2. Goroutine proliferation: runner_exec.go spawns exactly one goroutine
//     per pipe operator (left side). A pipeline of N commands produces N-1
//     goroutines, all simultaneously alive. Even goroutines blocked on
//     pipe I/O are tracked by the Go scheduler during work-stealing and by
//     the GC as live roots during every collection cycle. A deep pipeline
//     therefore degrades the entire host process (e.g. the datadog-agent),
//     not just the script being executed.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

// writeLinesFile writes a file containing nLines lines each of lineSize bytes
// (not counting the newline). It uses a streaming write to avoid holding the
// entire content in memory at test-setup time.
func writeLinesFile(t *testing.T, path string, nLines, lineSize int) {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	line := strings.Repeat("x", lineSize) + "\n"
	for i := 0; i < nLines; i++ {
		_, err := io.WriteString(f, line)
		require.NoError(t, err)
	}
}

// sortChainAlloc returns TotalAlloc delta (bytes ever allocated, including GC'd
// memory) while running the given sort-chain script against inputFile.
// Stdout is discarded so output buffering does not inflate the measurement.
func sortChainAlloc(t *testing.T, dir, script string) int64 {
	t.Helper()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, code := testutil.RunScriptDiscard(t, script, dir, interp.AllowedPaths([]string{dir}))
	runtime.ReadMemStats(&after)
	assert.Equal(t, 0, code, "sort chain exited non-zero")
	return int64(after.TotalAlloc) - int64(before.TotalAlloc)
}

// TestSortChainMemoryScalesWithDepth demonstrates that chaining N sort
// commands in a pipeline causes N × (input size) bytes to be allocated
// simultaneously, because each sort buffers its full input before it begins
// emitting output.
//
// With a 10 MiB input file:
//   - 1 sort  → ~10 MiB allocated
//   - 2 sorts → ~20 MiB allocated  (≈ 2×)
//   - 3 sorts → ~30 MiB allocated  (≈ 3×)
//
// The theoretical maximum with a 256 MiB input (MaxTotalBytes) and a 3-sort
// chain is 3 × 256 MiB = 768 MiB — well within a single script execution.
// There is no aggregate cap across all sorts in a pipeline.
func TestSortChainMemoryScalesWithDepth(t *testing.T) {
	dir := t.TempDir()
	inputFile := filepath.Join(dir, "input.txt")

	// 10 MiB input: 10240 lines × 1023 bytes each.
	// 1023 bytes keeps us safely below MaxLineBytes (1 MiB) and MaxLines (1M).
	const lineSize = 1023
	const nLines = 10240 // ≈ 10 MiB total
	writeLinesFile(t, inputFile, nLines, lineSize)

	type result struct {
		depth int
		alloc int64
	}

	depths := []int{1, 2, 3}
	results := make([]result, len(depths))

	for i, depth := range depths {
		// Build: sort input.txt | sort | sort ...
		parts := make([]string, depth)
		parts[0] = fmt.Sprintf("sort %s", inputFile)
		for j := 1; j < depth; j++ {
			parts[j] = "sort"
		}
		script := strings.Join(parts, " | ")
		alloc := sortChainAlloc(t, dir, script)
		results[i] = result{depth, alloc}
		t.Logf("sort chain depth %d: %d MiB allocated (%.1f MiB)", depth, alloc>>20, float64(alloc)/float64(1<<20))
	}

	// Each additional sort in the chain should add roughly the input size worth
	// of allocations. We allow a generous 50% tolerance to account for GC
	// timing and scanner/sort overhead.
	alloc1 := results[0].alloc
	alloc2 := results[1].alloc
	alloc3 := results[2].alloc

	t.Logf("Theoretical max (3 × 256 MiB): %d MiB", (3*256*1024*1024)>>20)
	t.Logf("alloc1=%d MiB, alloc2=%d MiB, alloc3=%d MiB", alloc1>>20, alloc2>>20, alloc3>>20)

	// alloc2 should be at least 1.5× alloc1 (generous lower bound for 2×).
	assert.GreaterOrEqual(t, alloc2, alloc1*3/2,
		"2-sort chain should allocate at least 1.5× as much as 1-sort chain")

	// alloc3 should be at least 2× alloc1 (generous lower bound for 3×).
	assert.GreaterOrEqual(t, alloc3, alloc1*2,
		"3-sort chain should allocate at least 2× as much as 1-sort chain")
}

// TestPipelineGoroutineScalesWithDepth demonstrates that a pipeline of N
// commands spawns N-1 goroutines that are all simultaneously alive, visible
// to the Go scheduler, and scanned by the GC on every collection cycle.
//
// Even though each goroutine is blocked on pipe I/O (doing no user work),
// the Go scheduler must track it during work-stealing decisions and the GC
// must scan its stack as a live root. With hundreds of concurrently-alive
// goroutines from a deep pipeline, this overhead degrades the entire host
// process — including unrelated datadog-agent goroutines running alongside.
//
// There is no enforced limit on pipeline depth today.
func TestPipelineGoroutineScalesWithDepth(t *testing.T) {
	depths := []int{5, 20, 100}

	for _, n := range depths {
		n := n
		t.Run(fmt.Sprintf("depth=%d", n), func(t *testing.T) {
			// Build: cat | cat | cat | ... (n commands → n-1 goroutines).
			// We use cat (reads stdin, writes stdout) so every left-side
			// goroutine blocks on its pipe read, keeping all N-1 goroutines
			// alive simultaneously while we sample the scheduler.
			cmds := make([]string, n)
			for i := range cmds {
				cmds[i] = "cat"
			}
			script := strings.Join(cmds, " | ")

			// Create a pipe for stdin. We keep the write end open so the
			// pipeline cannot drain — all N-1 goroutines stay blocked.
			pr, pw, err := os.Pipe()
			require.NoError(t, err)

			baselineGoroutines := runtime.NumGoroutine()

			var (
				wg     sync.WaitGroup
				peakG  int
				peakMu sync.Mutex
			)
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Sample goroutine count while the pipeline is running.
				// Sleep a moment so all goroutines have time to start.
				time.Sleep(20 * time.Millisecond)
				g := runtime.NumGoroutine()
				peakMu.Lock()
				peakG = g
				peakMu.Unlock()
				// Close the write end to unblock the pipeline.
				pw.Close()
			}()

			// Run the pipeline. stdin comes from pr so cat commands block
			// until pw is closed above.
			_, _ = testutil.RunScriptDiscard(t, script, t.TempDir(),
				interp.StdIO(pr, io.Discard, io.Discard),
			)
			pr.Close()
			wg.Wait()

			peakMu.Lock()
			peak := peakG
			peakMu.Unlock()

			delta := peak - baselineGoroutines
			// We expect at least N-1 additional goroutines (one per pipe left-side).
			// Use N/2 as the minimum to give generous slack for test-framework
			// goroutines that may appear or disappear between samples.
			minExpected := n / 2
			t.Logf("pipeline depth %d: baseline=%d, peak=%d, delta=%d (expected ≥%d goroutines simultaneously alive in scheduler)",
				n, baselineGoroutines, peak, delta, n-1)
			assert.GreaterOrEqual(t, delta, minExpected,
				"pipeline of %d commands should create at least %d simultaneously-live goroutines burdening the scheduler",
				n, minExpected)
		})
	}
}
