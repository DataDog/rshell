// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

// Config holds all runtime settings for the loop.
type Config struct {
	Model         string
	MaxTokens     int64
	MaxIterations int
	TargetSuccess int // consecutive clean checks required to stop
	WorkDir       string
	Verbose       bool
}

func main() {
	var (
		maxIter       = flag.Int("max-iterations", 30, "maximum loop iterations")
		targetSuccess = flag.Int("success-count", 5, "consecutive clean iterations required to stop")
		model         = flag.String("model", "claude-sonnet-4-6", "Anthropic model to use")
		maxTokens     = flag.Int("max-tokens", 32768, "max output tokens per agent call")
		workDir       = flag.String("dir", "", "repo root (default: auto-detect via git)")
		verbose       = flag.Bool("verbose", false, "show bash commands and their output")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: review-fix-loop [flags] [pr-number|pr-url]\n\n")
		fmt.Fprintf(os.Stderr, "Self-reviews a PR and iteratively fixes issues until clean.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		log.Fatal("ANTHROPIC_API_KEY environment variable is required")
	}

	dir := *workDir
	if dir == "" {
		var err error
		dir, err = gitRoot()
		if err != nil {
			log.Fatalf("detect git root: %v", err)
		}
	}

	cfg := Config{
		Model:         *model,
		MaxTokens:     int64(*maxTokens),
		MaxIterations: *maxIter,
		TargetSuccess: *targetSuccess,
		WorkDir:       dir,
		Verbose:       *verbose,
	}

	prRef := strings.Join(flag.Args(), " ")

	ctx := context.Background()
	if err := run(ctx, cfg, prRef); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func gitRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
