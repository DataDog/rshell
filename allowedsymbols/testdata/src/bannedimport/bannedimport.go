// Package bannedimport imports a permanently banned package.
package bannedimport

import (
	"fmt"
	"os/exec" // want `import of "os/exec" is permanently banned`
)

// Run uses a banned import.
func Run() {
	fmt.Println("run")
	_ = exec.Command("ls")
}
