// Package disallowedimport imports a package not in the allowlist.
package disallowedimport

import (
	"bufio" // want `import of "bufio" is not in the allowlist`
	"fmt"
)

// Read uses an import that is not allowlisted.
func Read() {
	fmt.Println("read")
	_ = bufio.NewScanner(nil)
}
