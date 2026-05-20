//go:build linux

package ip_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/internal/procnetroute"
	ipcmd "github.com/DataDog/rshell/builtins/ip"
)

func TestVulnHuntBuiltinIP_RouteProcPathTraversalRejected(t *testing.T) {
	procNetRouteMu.Lock()
	orig := ipcmd.ProcNetRoutePath
	ipcmd.ProcNetRoutePath = "/proc/../tmp"
	t.Cleanup(func() {
		ipcmd.ProcNetRoutePath = orig
		procNetRouteMu.Unlock()
	})

	stdout, stderr, code := cmdRun(t, "ip route show")
	assert.Equal(t, 1, code, "stdout=%q stderr=%q", stdout, stderr)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "unsafe procPath")
}

func TestVulnHuntBuiltinIP_RouteReaderHardFailsOnRouteCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n")
	for range procnetroute.MaxRoutes + 1 {
		b.WriteString("eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n")
	}
	writeProcNetRoute(t, b.String())

	stdout, stderr, code := cmdRun(t, "ip route show")
	require.Equal(t, 1, code, "stdout=%q stderr=%q", stdout, stderr)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "exceeded MaxRoutes")
}

func TestVulnHuntBuiltinIP_RouteReaderHardFailsOnTotalLineCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n")
	for range procnetroute.MaxTotalLines + 1 {
		b.WriteString("malformed\n")
	}
	writeProcNetRoute(t, b.String())

	stdout, stderr, code := cmdRun(t, "ip route show")
	require.Equal(t, 1, code, "stdout=%q stderr=%q", stdout, stderr)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "exceeded MaxTotalLines")
}
