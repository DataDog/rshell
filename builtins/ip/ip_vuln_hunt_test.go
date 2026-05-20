package ip

import (
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVulnHuntBuiltinIP_StaticImportSurface(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "ip.go", nil, parser.ImportsOnly)
	require.NoError(t, err)

	imports := map[string]bool{}
	for _, spec := range f.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		require.NoError(t, err)
		imports[path] = true
	}

	require.False(t, imports["os"], "ip.go must not open script-controlled files directly")
	require.False(t, imports["io/ioutil"], "ip.go must not use legacy direct file reads")
	require.False(t, imports["github.com/DataDog/rshell/allowedpaths"], "ip must not derive route reads from AllowedPaths")
	require.True(t, imports["github.com/DataDog/rshell/builtins/internal/procnetroute"], "route reads must stay behind the audited procnetroute helper")
}
