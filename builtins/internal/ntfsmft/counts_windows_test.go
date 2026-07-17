// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package ntfsmft

import (
	"path/filepath"
	"testing"
)

// Bucket file/dir counts follow the same walk-up attribution as byte totals:
// a file anywhere in a child's subtree counts toward that child, and Dirs is
// the number of descendant directories (excluding the child itself).
func TestScan_CountsBuckets(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "A", "f1.bin"), make([]byte, 4096))
	writeFile(t, filepath.Join(root, "A", "f2.bin"), make([]byte, 4096))
	writeFile(t, filepath.Join(root, "A", "sub", "f3.bin"), make([]byte, 4096))
	writeFile(t, filepath.Join(root, "B", "f4.bin"), make([]byte, 4096))

	res := scanOrSkip(t, root, Options{})

	a := findBucket(t, res, "A")
	if a.Files != 3 {
		t.Errorf("bucket A Files = %d, want 3 (f1, f2, sub/f3)", a.Files)
	}
	if a.Dirs != 1 {
		t.Errorf("bucket A Dirs = %d, want 1 (sub)", a.Dirs)
	}
	b := findBucket(t, res, "B")
	if b.Files != 1 {
		t.Errorf("bucket B Files = %d, want 1", b.Files)
	}
	if b.Dirs != 0 {
		t.Errorf("bucket B Dirs = %d, want 0", b.Dirs)
	}
}

// In tree mode the counts are cumulative like Size: deep files/dirs roll up into
// their in-tree ancestors, and the root node totals the whole in-scope subtree.
func TestScan_CountsTreeRollup(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "A", "f1.bin"), make([]byte, 4096))
	writeFile(t, filepath.Join(root, "A", "sub", "deep", "f2.bin"), make([]byte, 4096))
	writeFile(t, filepath.Join(root, "B", "f3.bin"), make([]byte, 4096))
	writeFile(t, filepath.Join(root, "loose.bin"), make([]byte, 4096))

	res := scanOrSkip(t, root, Options{TreeDepth: 1})
	if res.Tree == nil {
		t.Fatal("Tree is nil at TreeDepth 1")
	}

	// Root totals: all 4 files (incl. loose) and all 4 descendant dirs
	// (A, A/sub, A/sub/deep, B).
	if res.Tree.Files != 4 {
		t.Errorf("root Files = %d, want 4", res.Tree.Files)
	}
	if res.Tree.Dirs != 4 {
		t.Errorf("root Dirs = %d, want 4 (A, A/sub, A/sub/deep, B)", res.Tree.Dirs)
	}

	a := findTreeChild(t, res.Tree, "A")
	if a.Files != 2 {
		t.Errorf("A Files = %d, want 2 (f1 + rolled-up deep f2)", a.Files)
	}
	if a.Dirs != 2 {
		t.Errorf("A Dirs = %d, want 2 (sub, sub/deep)", a.Dirs)
	}
	b := findTreeChild(t, res.Tree, "B")
	if b.Files != 1 || b.Dirs != 0 {
		t.Errorf("B Files/Dirs = %d/%d, want 1/0", b.Files, b.Dirs)
	}
}

// A hardlinked file within a single bucket is counted once for that bucket,
// mirroring the byte-total dedup.
func TestScan_CountsHardlinkSameBucket(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "A", "primary.bin")
	writeFile(t, primary, make([]byte, 4096))
	createHardLink(t, filepath.Join(root, "A", "alias.bin"), primary)

	res := scanOrSkip(t, root, Options{})
	if got := findBucket(t, res, "A").Files; got != 1 {
		t.Errorf("bucket A Files = %d, want 1 (hardlink counted once)", got)
	}
}

// A file hardlinked across two buckets counts once in each, mirroring how its
// bytes are attributed to both.
func TestScan_CountsHardlinkAcrossBuckets(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "A", "primary.bin")
	writeFile(t, primary, make([]byte, 4096))
	createHardLink(t, filepath.Join(root, "B", "alias.bin"), primary)

	res := scanOrSkip(t, root, Options{})
	if got := findBucket(t, res, "A").Files; got != 1 {
		t.Errorf("bucket A Files = %d, want 1", got)
	}
	if got := findBucket(t, res, "B").Files; got != 1 {
		t.Errorf("bucket B Files = %d, want 1", got)
	}
}

// Excluded subtrees are omitted from counts exactly as they are from byte totals.
func TestScan_CountsExcludeRespected(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "A", "keep.bin"), make([]byte, 4096))
	drop := filepath.Join(root, "A", "skip")
	writeFile(t, filepath.Join(drop, "gone.bin"), make([]byte, 4096))

	res := scanOrSkip(t, root, Options{Exclude: []string{drop}})
	a := findBucket(t, res, "A")
	if a.Files != 1 {
		t.Errorf("bucket A Files = %d, want 1 (excluded file omitted)", a.Files)
	}
	if a.Dirs != 0 {
		t.Errorf("bucket A Dirs = %d, want 0 (excluded 'skip' dir omitted)", a.Dirs)
	}
}

// Depth-0 bucket counts and the TreeDepth-1 depth-1 projection must agree, the
// same invariant the engine maintains for Size.
func TestScan_CountsDepth0MatchesTree(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "A", "f1.bin"), make([]byte, 4096))
	writeFile(t, filepath.Join(root, "A", "sub", "f2.bin"), make([]byte, 4096))
	writeFile(t, filepath.Join(root, "B", "sub2", "deep", "f3.bin"), make([]byte, 4096))

	flat := scanOrSkip(t, root, Options{})
	tree := scanOrSkip(t, root, Options{TreeDepth: 1})

	for _, name := range []string{"A", "B"} {
		fb := findBucket(t, flat, name)
		tb := findBucket(t, tree, name)
		if fb.Files != tb.Files || fb.Dirs != tb.Dirs {
			t.Errorf("bucket %s counts differ: depth0 %d/%d vs tree %d/%d",
				name, fb.Files, fb.Dirs, tb.Files, tb.Dirs)
		}
	}
}
