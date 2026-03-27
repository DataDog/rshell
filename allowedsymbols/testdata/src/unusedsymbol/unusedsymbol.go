// Package unusedsymbol has an allowlisted symbol that is never used.
package unusedsymbol // want `allowlist symbol "fmt.Sprintf" is not used`

import "fmt"

// Hello uses only fmt.Println; fmt.Sprintf is in the allowlist but unused.
func Hello() {
	fmt.Println("hello")
}
