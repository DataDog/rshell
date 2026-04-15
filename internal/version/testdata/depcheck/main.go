// Program depcheck prints the rshell version as seen from an external module
// that imports rshell as a dependency. Used by TestBuildVersionAsDependency.
package main

import (
	"fmt"
	"runtime/debug"

	_ "github.com/DataDog/rshell/interp" // ensure rshell appears in deps
)

func main() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		fmt.Println("NO_BUILD_INFO")
		return
	}
	for _, dep := range info.Deps {
		if dep.Path == "github.com/DataDog/rshell" {
			fmt.Println(dep.Version)
			return
		}
	}
	fmt.Println("NOT_FOUND")
}
