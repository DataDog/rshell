// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package ntfsdu

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/ntfsmft"
)

const (
	modeAllocated = "allocated"
	modeApparent  = "apparent"
)

// jsonBucket is one immediate-child directory of the target.
type jsonBucket struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"` // "dir" or "reparse"
	SizeBytes   int64  `json:"sizeBytes"`
	FileCount   int    `json:"fileCount"`
	FolderCount int    `json:"folderCount"`
}

// jsonTreeNode is one node in the depth-limited directory tree. The tree is
// emitted as a flat, pre-order list; Path is the full path to the node and
// Pruned marks a leaf at the requested depth that has undisplayed descendants.
type jsonTreeNode struct {
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	SizeBytes   int64  `json:"sizeBytes"`
	Pruned      bool   `json:"pruned"`
	FileCount   int    `json:"fileCount"`
	FolderCount int    `json:"folderCount"`
}

// jsonFileEntry is one file in topFiles / find matches.
type jsonFileEntry struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"sizeBytes"`
}

// jsonExtEntry is one aggregated file extension.
type jsonExtEntry struct {
	Ext       string `json:"ext"`
	SizeBytes int64  `json:"sizeBytes"`
	FileCount int    `json:"fileCount"`
}

// jsonFindQuery echoes the find predicate back in the output.
type jsonFindQuery struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	Limit int    `json:"limit,omitempty"`
	Label string `json:"label,omitempty"`
}

// jsonFindBlock pairs a find query with its matches.
type jsonFindBlock struct {
	Query   jsonFindQuery   `json:"query"`
	Matches []jsonFileEntry `json:"matches"`
}

// jsonOutput is the top-level JSON document ntfs-du emits.
type jsonOutput struct {
	Target       string          `json:"target"`
	Mode         string          `json:"mode"`
	SubtreeBytes int64           `json:"subtreeBytes"`
	Buckets      []jsonBucket    `json:"buckets"`
	Tree         []jsonTreeNode  `json:"tree"`
	TopFiles     []jsonFileEntry `json:"topFiles"`
	TopExt       []jsonExtEntry  `json:"topExt"`
	FindResults  []jsonFindBlock `json:"findResults"`
}

// run performs the scan on Windows and writes the JSON report to stdout.
func run(ctx context.Context, callCtx *builtins.CallContext, opts options) builtins.Result {
	target := opts.target
	if target == "" {
		target = driveRoot(callCtx.WorkDir())
	}

	finds := buildFinds(opts)

	mode := modeAllocated
	if opts.apparent {
		mode = modeApparent
	}

	res, err := ntfsmft.Scan(ctx, target, ntfsmft.Options{
		ShowApparent:  opts.apparent,
		TopFiles:      opts.topFiles,
		TopExtensions: opts.topExt,
		MinFileSize:   opts.minSize,
		Finds:         finds,
		Exclude:       opts.exclude,
		TreeDepth:     opts.maxDepth,
		TreeMinSize:   opts.minSize,
	})
	if err != nil {
		callCtx.Errf("ntfs-du: %s\n", err)
		return builtins.Result{Code: 1}
	}

	out := buildOutput(res, mode, opts.maxDepth)
	enc, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		callCtx.Errf("ntfs-du: encoding output: %s\n", err)
		return builtins.Result{Code: 1}
	}
	callCtx.Out(string(enc))
	callCtx.Out("\n")
	return builtins.Result{}
}

// driveRoot returns the volume root ("C:\") for a drive-letter path. Non
// drive-shaped inputs are returned unchanged so Scan surfaces a clear error.
func driveRoot(wd string) string {
	if len(wd) >= 2 && wd[1] == ':' {
		return wd[:2] + `\`
	}
	return wd
}

// buildFinds translates the per-type find flags into ntfsmft.FindQuery values,
// preserving the order ext, glob, regex.
func buildFinds(opts options) []ntfsmft.FindQuery {
	total := len(opts.findExt) + len(opts.findGlob) + len(opts.findRegex)
	if total == 0 {
		return nil
	}
	finds := make([]ntfsmft.FindQuery, 0, total)
	for _, v := range opts.findExt {
		finds = append(finds, ntfsmft.FindQuery{Type: "ext", Value: v, Limit: opts.findLimit})
	}
	for _, v := range opts.findGlob {
		finds = append(finds, ntfsmft.FindQuery{Type: "glob", Value: v, Limit: opts.findLimit})
	}
	for _, v := range opts.findRegex {
		finds = append(finds, ntfsmft.FindQuery{Type: "regex", Value: v, Limit: opts.findLimit})
	}
	return finds
}

// buildOutput maps an ntfsmft.Result into the JSON document.
// depth is the requested tree depth, used to mark pruned leaves.
func buildOutput(res *ntfsmft.Result, mode string, depth int) jsonOutput {
	out := jsonOutput{
		Target:       res.Target,
		Mode:         mode,
		SubtreeBytes: res.Subtree,
		Buckets:      make([]jsonBucket, 0, len(res.Buckets)),
		Tree:         []jsonTreeNode{},
		TopFiles:     make([]jsonFileEntry, 0, len(res.TopFiles)),
		TopExt:       make([]jsonExtEntry, 0, len(res.TopExtensions)),
		FindResults:  make([]jsonFindBlock, 0, len(res.FindResults)),
	}

	for _, b := range res.Buckets {
		out.Buckets = append(out.Buckets, jsonBucket{
			Name:        b.Name,
			Kind:        dirKind(b.Reparse),
			SizeBytes:   b.Size,
			FileCount:   b.Files,
			FolderCount: b.Dirs,
		})
	}

	if res.Tree != nil {
		out.Tree = flattenTree(res.Tree, depth)
	}

	for _, f := range res.TopFiles {
		out.TopFiles = append(out.TopFiles, jsonFileEntry{Path: f.Path, SizeBytes: f.Size})
	}
	for _, e := range res.TopExtensions {
		out.TopExt = append(out.TopExt, jsonExtEntry{Ext: e.Ext, SizeBytes: e.Size, FileCount: e.Count})
	}
	for _, blk := range res.FindResults {
		matches := make([]jsonFileEntry, 0, len(blk.Matches))
		for _, m := range blk.Matches {
			matches = append(matches, jsonFileEntry{Path: m.Path, SizeBytes: m.Size})
		}
		out.FindResults = append(out.FindResults, jsonFindBlock{
			Query: jsonFindQuery{
				Type:  blk.Query.Type,
				Value: blk.Query.Value,
				Limit: blk.Query.Limit,
				Label: blk.Query.Label,
			},
			Matches: matches,
		})
	}

	return out
}

// flattenTree walks an ntfsmft.TreeNode subtree in pre-order and returns a flat
// list of nodes. The root node carries its full path; descendants carry a path
// joined from the parent's path and the node basename. A node at the requested
// depth with no displayed children is marked Pruned.
func flattenTree(root *ntfsmft.TreeNode, depth int) []jsonTreeNode {
	var nodes []jsonTreeNode
	var walk func(n *ntfsmft.TreeNode, parentPath string)
	walk = func(n *ntfsmft.TreeNode, parentPath string) {
		path := n.Name
		if n.Depth != 0 {
			path = filepath.Join(parentPath, n.Name)
		}
		nodes = append(nodes, jsonTreeNode{
			Path:        path,
			Kind:        dirKind(n.Reparse),
			SizeBytes:   n.Size,
			Pruned:      n.Depth == depth && len(n.Children) == 0,
			FileCount:   n.Files,
			FolderCount: n.Dirs,
		})
		for _, c := range n.Children {
			walk(c, path)
		}
	}
	walk(root, "")
	return nodes
}

func dirKind(reparse bool) string {
	if reparse {
		return "reparse"
	}
	return "dir"
}
