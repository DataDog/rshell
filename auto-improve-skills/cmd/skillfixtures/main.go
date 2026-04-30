// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"fmt"
	"os"

	"github.com/DataDog/rshell/auto-improve-skills/internal/autoresearch"
)

func main() {
	root, err := autoresearch.RepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "skillfixtures: %v\n", err)
		os.Exit(1)
	}
	if err := autoresearch.GenerateRemoteHostDiagnosticsFixtures(root); err != nil {
		fmt.Fprintf(os.Stderr, "skillfixtures: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(autoresearch.RemoteHostDiagnosticsGeneratedFixtureRoot(root))
}
