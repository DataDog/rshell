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
