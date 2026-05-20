package ip_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVulnHuntBuiltinIP_DangerousFlagsFailClosed(t *testing.T) {
	cases := []string{
		"ip -b /tmp/cmds addr show",
		"ip -B addr show",
		"ip -batch /tmp/cmds addr show",
		"ip --batch /tmp/cmds addr show",
		"ip --force addr show",
		"ip -force addr show",
		"ip -n ns addr show",
		"ip --netns ns addr show",
		"ip -br addr show",
		"ip -h -n ns addr show",
		"ip netns exec ns sh",
		"ip -- netns exec ns sh",
	}

	for _, script := range cases {
		t.Run(script, func(t *testing.T) {
			stdout, stderr, code := cmdRun(t, script)
			assert.Equal(t, 1, code, "script=%q stdout=%q stderr=%q", script, stdout, stderr)
			assert.Empty(t, stdout)
			assert.NotEmpty(t, stderr)
		})
	}
}

func TestVulnHuntBuiltinIP_WriteVerbsFailClosed(t *testing.T) {
	cases := []struct {
		script string
		want   string
		code   int
	}{
		{`ip addr add 10.0.0.1/24 dev lo`, "write operations", 1},
		{`ip address append 10.0.0.1/24 dev lo`, "write operations", 1},
		{`ip addr show add 10.0.0.1/24`, "unknown token", 1},
		{`ip link set lo up`, "write operations", 1},
		{`ip link show set lo up`, "unknown token", 1},
		{`ip route add default via 1.1.1.1`, "write operations", 1},
		{`ip route -- add default via 1.1.1.1`, "write operations", 1},
		{`ip route save /tmp/routes`, "write operations", 1},
		{`ip route help`, "is unknown", 255},
	}

	for _, tc := range cases {
		t.Run(tc.script, func(t *testing.T) {
			stdout, stderr, code := cmdRun(t, tc.script)
			assert.Equal(t, tc.code, code, "stdout=%q stderr=%q", stdout, stderr)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, tc.want)
		})
	}
}

func TestVulnHuntBuiltinIP_DiagnosticsQuoteControlChars(t *testing.T) {
	cases := []struct {
		script      string
		wantEscaped string
		code        int
	}{
		{"ip \"bad\nobject\"", `bad\nobject`, 1},
		{"ip addr show dev \"lo\nFORGED\"", `lo\nFORGED`, 1},
		{"ip addr show \"token\nFORGED\"", `token\nFORGED`, 1},
		{"ip route \"show\nFORGED\"", `show\nFORGED`, 255},
		{"ip route get \"1.2.3.4\nFORGED\"", `1.2.3.4\nFORGED`, 1},
	}

	for _, tc := range cases {
		t.Run(strings.ReplaceAll(tc.wantEscaped, `\n`, "_"), func(t *testing.T) {
			stdout, stderr, code := cmdRun(t, tc.script)
			require.Equal(t, tc.code, code, "stdout=%q stderr=%q", stdout, stderr)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, tc.wantEscaped)
			raw := strings.ReplaceAll(tc.wantEscaped, `\n`, "\n")
			assert.NotContains(t, strings.TrimSuffix(stderr, "\n"), raw, "diagnostic should quote embedded newlines")
		})
	}
}

func TestVulnHuntBuiltinIP_RouteArgumentsFailBeforeProcRead(t *testing.T) {
	cases := []struct {
		script string
		want   string
	}{
		{`ip route get 001.2.3.4`, "invalid address"},
		{`ip route get 256.0.0.1`, "invalid address"},
		{`ip route get 1.2.3.4.5`, "invalid address"},
		{`ip route get ::1`, "invalid address"},
		{`ip route get 1.2.3.4 extra`, "unsupported argument"},
		{`ip -6 route show`, "IPv6 routing not supported"},
		{`ip -o route show`, "not supported for route output"},
		{`ip --brief route show`, "not supported for route output"},
	}

	for _, tc := range cases {
		t.Run(tc.script, func(t *testing.T) {
			stdout, stderr, code := cmdRun(t, tc.script)
			assert.Equal(t, 1, code, "stdout=%q stderr=%q", stdout, stderr)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, tc.want)
		})
	}
}
