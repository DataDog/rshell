// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package ip implements the ip builtin command.
//
// ip — show network interfaces, addresses, and routing
//
// Usage: ip [GLOBAL-OPTIONS] OBJECT [COMMAND [ARGUMENTS]]
//
// Query network interface and routing information. Only read-only subcommands
// are supported. All write operations (add, del, flush, change, replace, set)
// and dangerous execution vectors (netns exec, -batch, -force) are rejected
// with exit code 1.
//
// GLOBAL OPTIONS
//
//	-o, --oneline
//	    Output each record on a single line; internal newlines are represented
//	    by a backslash followed by the continuation content (matching real ip
//	    -o format). Useful for machine parsing by AI agents.
//
//	--brief
//	    Print a compact tabular summary: interface name, state, and addresses
//	    only. Mutually compatible with -4/-6. (Note: the real ip command uses
//	    -br as a shorthand; our builtin uses --brief instead.)
//
//	-4
//	    Restrict output to IPv4 only (for addr/link; route always uses IPv4).
//
//	-6
//	    Restrict address output to IPv6 only. Not supported for route.
//
//	-h, --help
//	    Print this usage message to stdout and exit 0.
//
// OBJECTS AND COMMANDS
//
//	addr [show] [dev IFNAME]
//	    Show IP addresses assigned to all network interfaces, or to the
//	    single interface named IFNAME when "dev IFNAME" is given.
//	    "show" is the default command when no command is specified.
//
//	link [show] [dev IFNAME]
//	    Show link-layer information (MTU, hardware address, flags) for all
//	    interfaces, or for the single interface named IFNAME.
//	    "show" is the default command when no command is specified.
//
//	route [show|list]
//	    Show the IPv4 routing table, read from /proc/net/route.
//	    Only supported on Linux; returns an error on other platforms.
//
//	route get ADDRESS
//	    Show the route that would be used to reach ADDRESS, selected by
//	    longest-prefix-match over the IPv4 routing table.
//	    Only supported on Linux; returns an error on other platforms.
//
// BLOCKED FLAGS AND SUBCOMMANDS (exit 1 with an explanatory error)
//
//	-b, -B, -batch      Reads ip commands from FILE — arbitrary command
//	                    execution vector (GTFOBins).
//	-force              Suppresses errors; companion to -batch (GTFOBins).
//	-n, --netns         Switches network namespace — privilege escalation.
//	ip netns            Network namespace management — shell escape via
//	                    "ip netns exec <ns> <cmd>".
//	addr add/del/flush/change/replace    Write operations (blocked).
//	link set/add/del/change              Write operations (blocked).
//	route add/del/delete/change/replace  Write operations (blocked).
//	route flush/save/restore             Write operations (blocked).
//
// Exit codes:
//
//	0  Query completed successfully.
//	1  Unknown subcommand, unsupported flag, write operation attempted,
//	   unsupported platform (route), or the named interface does not exist.
//
// Network access:
//
//	addr and link use Go's net.Interfaces() for read-only enumeration of OS
//	network interfaces and their addresses; the AllowedPaths sandbox is not
//	involved. route reads /proc/net/route via callCtx.OpenFile (Linux only).
//
// Memory safety for route:
//
//	/proc/net/route is read line-by-line with a per-line cap of MaxLineBytes
//	(1 MiB). At most maxRoutes (10 000) entries are loaded. All read loops
//	check ctx.Err() at each iteration to honour the execution timeout.
//
// Output differences from real ip:
//
//	The qdisc field is omitted from interface header lines. For route, the
//	proto/scope/src fields are not included in the output (not available from
//	/proc/net/route alone).
package ip

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/DataDog/rshell/builtins"
)

// ProcNetRoutePath is the path to the kernel IPv4 routing table.
// It is a package-level variable so tests can point it at a synthetic file
// instead of the real /proc/net/route.
var ProcNetRoutePath = "/proc/net/route"

// MaxLineBytes is the per-line buffer cap for the route-table scanner.
// Lines longer than this are dropped rather than causing an unbounded allocation.
const MaxLineBytes = 1 << 20 // 1 MiB

const (
	routeScanBufInit = 4096
	maxRoutes        = 10_000 // cap to prevent memory exhaustion from a crafted file
)

// Routing table flags (from linux/route.h).
const (
	rtfUp      = uint32(0x0001)
	rtfGateway = uint32(0x0002)
)

// routeEntry holds a parsed entry from /proc/net/route.
// IP address fields use the same little-endian uint32 encoding as the file:
// for 192.168.1.1 the stored value is 0x0101A8C0 and
// hexToIPStr(0x0101A8C0) returns "192.168.1.1".
type routeEntry struct {
	iface  string
	dest   uint32
	gw     uint32
	flags  uint32
	metric uint32
	mask   uint32
}

// Cmd is the ip builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "ip",
	Description: "show network interface information",
	MakeFlags:   registerFlags,
}

// displayOpts holds the resolved global display options.
type displayOpts struct {
	oneline bool
	brief   bool
	ipv4    bool
	ipv6    bool
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := fs.BoolP("help", "h", false, "print usage and exit")
	oneline := fs.BoolP("oneline", "o", false, "output each record on a single line")
	brief := fs.Bool("brief", false, "print brief information in tabular format")
	ipv4 := fs.BoolP("ipv4", "4", false, "show only IPv4 addresses")
	ipv6 := fs.BoolP("ipv6", "6", false, "show only IPv6 addresses")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			printHelp(callCtx, fs)
			return builtins.Result{}
		}

		do := displayOpts{
			oneline: *oneline,
			brief:   *brief,
			ipv4:    *ipv4,
			ipv6:    *ipv6,
		}

		// If both -4 and -6 are given, neither filter applies.
		if do.ipv4 && do.ipv6 {
			do.ipv4 = false
			do.ipv6 = false
		}

		if len(args) == 0 {
			callCtx.Errf("ip: object required\nTry 'ip --help' for more information.\n")
			return builtins.Result{Code: 1}
		}

		object, rest := args[0], args[1:]

		switch object {
		case "addr", "address":
			return runAddr(ctx, callCtx, do, rest)
		case "link":
			return runLink(ctx, callCtx, do, rest)
		case "route":
			return routeCmd(ctx, callCtx, do, rest)
		case "netns":
			callCtx.Errf("ip: 'netns' subcommand is blocked (shell escape vector via 'ip netns exec')\n")
			return builtins.Result{Code: 1}
		default:
			callCtx.Errf("ip: object %q is not supported\nSupported objects: addr, link, route\n", object)
			return builtins.Result{Code: 1}
		}
	}
}

// printHelp writes the usage text to stdout.
func printHelp(callCtx *builtins.CallContext, fs *builtins.FlagSet) {
	callCtx.Out("Usage: ip [GLOBAL-OPTIONS] OBJECT [COMMAND [ARGUMENTS]]\n")
	callCtx.Out("Show network interface and routing information.\n\n")
	callCtx.Out("Supported objects:\n")
	callCtx.Out("  addr [show] [dev IFNAME]  Show IP addresses\n")
	callCtx.Out("  link [show] [dev IFNAME]  Show link-layer information\n")
	callCtx.Out("  route [show|list]         Show IPv4 routing table (Linux only)\n")
	callCtx.Out("  route get ADDRESS         Show route to ADDRESS (Linux only)\n\n")
	callCtx.Out("Global options:\n")
	fs.SetOutput(callCtx.Stdout)
	fs.PrintDefaults()
	callCtx.Out("\nNote: -b/-B/-batch, -force, -n/--netns, and 'ip netns' are blocked for safety.\n")
	callCtx.Out("Note: the real ip command's -br flag is --brief in this builtin.\n")
}

// parseShowArgs parses the argument list after an object name ("addr" or "link").
// Returns (devFilter, error). devFilter="" means show all interfaces.
// Write subcommands return a descriptive error.
func parseShowArgs(object string, args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}

	// Consume optional "show"/"list"/"lst" command keyword.
	switch args[0] {
	case "show", "list", "lst":
		args = args[1:]
	case "add", "append", "replace", "del", "delete", "flush", "set", "change":
		return "", fmt.Errorf("'ip %s %s' is not supported (write operations are blocked for safety)", object, args[0])
	}

	// Parse optional "dev IFNAME" filter; any other token is an error.
	var devFilter string
	for len(args) > 0 {
		switch args[0] {
		case "dev":
			if len(args) < 2 {
				return "", fmt.Errorf("'dev' requires an interface name argument")
			}
			devFilter = args[1]
			args = args[2:]
		default:
			return "", fmt.Errorf("unknown token %q after 'ip %s show'", args[0], object)
		}
	}
	return devFilter, nil
}

// getInterfaces returns all interfaces, optionally filtered by devName.
// If devName is set and no matching interface is found, returns an error.
func getInterfaces(devName string) ([]net.Interface, error) {
	all, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("cannot enumerate interfaces: %w", err)
	}
	if devName == "" {
		return all, nil
	}
	for _, iface := range all {
		if iface.Name == devName {
			return []net.Interface{iface}, nil
		}
	}
	return nil, fmt.Errorf("cannot find device %q", devName)
}

// flagsStr returns the kernel flags string, e.g. "<BROADCAST,MULTICAST,UP,LOWER_UP>".
func flagsStr(flags net.Flags) string {
	var parts []string
	if flags&net.FlagBroadcast != 0 {
		parts = append(parts, "BROADCAST")
	}
	if flags&net.FlagMulticast != 0 {
		parts = append(parts, "MULTICAST")
	}
	if flags&net.FlagLoopback != 0 {
		parts = append(parts, "LOOPBACK")
	}
	if flags&net.FlagPointToPoint != 0 {
		parts = append(parts, "POINTOPOINT")
	}
	if flags&net.FlagUp != 0 {
		parts = append(parts, "UP")
	}
	if flags&net.FlagRunning != 0 {
		parts = append(parts, "LOWER_UP")
	}
	return "<" + strings.Join(parts, ",") + ">"
}

// ifaceState derives the ip-style state string from interface flags.
func ifaceState(flags net.Flags) string {
	if flags&net.FlagLoopback != 0 {
		// Loopback interfaces lack a physical carrier, so ip reports UNKNOWN.
		return "UNKNOWN"
	}
	up := flags&net.FlagUp != 0
	running := flags&net.FlagRunning != 0
	switch {
	case up && running:
		return "UP"
	case !up:
		return "DOWN"
	default:
		return "UNKNOWN"
	}
}

// ifaceLinkType returns the "link/TYPE" label for an interface.
func ifaceLinkType(iface net.Interface) string {
	switch {
	case iface.Flags&net.FlagLoopback != 0:
		return "loopback"
	case len(iface.HardwareAddr) == 6:
		return "ether"
	case iface.Flags&net.FlagPointToPoint != 0:
		return "ppp"
	default:
		return "none"
	}
}

// ifaceMAC returns the interface MAC address as a colon-hex string.
// Returns "00:00:00:00:00:00" when the hardware address is absent.
func ifaceMAC(iface net.Interface) string {
	s := iface.HardwareAddr.String()
	if s == "" {
		return "00:00:00:00:00:00"
	}
	return s
}

// ifaceBrdMAC returns the broadcast MAC for an interface, or "" if the
// interface type does not use a broadcast address.
func ifaceBrdMAC(iface net.Interface) string {
	switch {
	case iface.Flags&net.FlagLoopback != 0:
		return "00:00:00:00:00:00"
	case iface.Flags&net.FlagBroadcast != 0:
		return "ff:ff:ff:ff:ff:ff"
	default:
		return ""
	}
}

// ipv4Broadcast computes the IPv4 broadcast address for ipNet.
// Returns "" for IPv6 networks.
func ipv4Broadcast(ipNet *net.IPNet) string {
	ip4 := ipNet.IP.To4()
	if ip4 == nil {
		return ""
	}
	mask := ipNet.Mask
	brd := make(net.IP, 4)
	for i := range 4 {
		brd[i] = ip4[i] | ^mask[i]
	}
	return brd.String()
}

// addrScope returns the RTSCOPE name for an IP address.
func addrScope(ip net.IP) string {
	if ip.IsLoopback() {
		return "host"
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return "link"
	}
	return "global"
}

// runAddr implements "ip addr [show] [dev IFNAME]".
func runAddr(ctx context.Context, callCtx *builtins.CallContext, do displayOpts, args []string) builtins.Result {
	devFilter, err := parseShowArgs("addr", args)
	if err != nil {
		callCtx.Errf("ip: %s\n", err)
		return builtins.Result{Code: 1}
	}

	ifaces, err := getInterfaces(devFilter)
	if err != nil {
		callCtx.Errf("ip: %s\n", err)
		return builtins.Result{Code: 1}
	}

	for _, iface := range ifaces {
		if ctx.Err() != nil {
			break
		}
		if err := printAddrEntry(callCtx, do, iface); err != nil {
			callCtx.Errf("ip: %s\n", err)
			return builtins.Result{Code: 1}
		}
	}
	return builtins.Result{}
}

// runLink implements "ip link [show] [dev IFNAME]".
func runLink(ctx context.Context, callCtx *builtins.CallContext, do displayOpts, args []string) builtins.Result {
	devFilter, err := parseShowArgs("link", args)
	if err != nil {
		callCtx.Errf("ip: %s\n", err)
		return builtins.Result{Code: 1}
	}

	ifaces, err := getInterfaces(devFilter)
	if err != nil {
		callCtx.Errf("ip: %s\n", err)
		return builtins.Result{Code: 1}
	}

	for _, iface := range ifaces {
		if ctx.Err() != nil {
			break
		}
		printLinkEntry(callCtx, do, iface)
	}
	return builtins.Result{}
}

// printAddrEntry renders one interface's address information.
func printAddrEntry(callCtx *builtins.CallContext, do displayOpts, iface net.Interface) error {
	addrs, err := iface.Addrs()
	if err != nil {
		return fmt.Errorf("cannot get addresses for %s: %w", iface.Name, err)
	}

	state := ifaceState(iface.Flags)
	mac := ifaceMAC(iface)
	brdMAC := ifaceBrdMAC(iface)
	ltype := ifaceLinkType(iface)
	flags := flagsStr(iface.Flags)

	if do.brief {
		var addrParts []string
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP
			if do.ipv4 && ip.To4() == nil {
				continue
			}
			if do.ipv6 && ip.To4() != nil {
				continue
			}
			addrParts = append(addrParts, ipNet.String())
		}
		addrStr := strings.Join(addrParts, " ")
		if addrStr != "" {
			addrStr = " " + addrStr
		}
		callCtx.Outf("%-16s %-13s%s\n", iface.Name, state, addrStr)
		return nil
	}

	if do.oneline {
		// Oneline mode: one output line per address (no interface header line).
		prefix := fmt.Sprintf("%d: %s", iface.Index, iface.Name)
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP
			if do.ipv4 && ip.To4() == nil {
				continue
			}
			if do.ipv6 && ip.To4() != nil {
				continue
			}
			var addrLine string
			if ip.To4() != nil {
				brd := ipv4Broadcast(ipNet)
				if brd != "" && iface.Flags&net.FlagBroadcast != 0 {
					addrLine = fmt.Sprintf("    inet %s brd %s scope %s %s",
						ipNet.String(), brd, addrScope(ip), iface.Name)
				} else {
					addrLine = fmt.Sprintf("    inet %s scope %s %s",
						ipNet.String(), addrScope(ip), iface.Name)
				}
			} else {
				addrLine = fmt.Sprintf("    inet6 %s scope %s",
					ipNet.String(), addrScope(ip))
			}
			callCtx.Outf("%s%s\\       valid_lft forever preferred_lft forever\n",
				prefix, addrLine)
		}
		return nil
	}

	// Normal multi-line output.
	callCtx.Outf("%d: %s: %s mtu %d state %s group default qlen 1000\n",
		iface.Index, iface.Name, flags, iface.MTU, state)
	if brdMAC != "" {
		callCtx.Outf("    link/%s %s brd %s\n", ltype, mac, brdMAC)
	} else {
		callCtx.Outf("    link/%s %s\n", ltype, mac)
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP
		if do.ipv4 && ip.To4() == nil {
			continue
		}
		if do.ipv6 && ip.To4() != nil {
			continue
		}
		scope := addrScope(ip)
		if ip.To4() != nil {
			brd := ipv4Broadcast(ipNet)
			if brd != "" && iface.Flags&net.FlagBroadcast != 0 {
				callCtx.Outf("    inet %s brd %s scope %s %s\n",
					ipNet.String(), brd, scope, iface.Name)
			} else {
				callCtx.Outf("    inet %s scope %s %s\n",
					ipNet.String(), scope, iface.Name)
			}
		} else {
			callCtx.Outf("    inet6 %s scope %s\n", ipNet.String(), scope)
		}
		callCtx.Out("       valid_lft forever preferred_lft forever\n")
	}
	return nil
}

// printLinkEntry renders one interface's link-layer information.
func printLinkEntry(callCtx *builtins.CallContext, do displayOpts, iface net.Interface) {
	state := ifaceState(iface.Flags)
	mac := ifaceMAC(iface)
	brdMAC := ifaceBrdMAC(iface)
	ltype := ifaceLinkType(iface)
	flags := flagsStr(iface.Flags)

	if do.brief {
		callCtx.Outf("%-16s %-13s %s %s\n", iface.Name, state, mac, flags)
		return
	}

	headerLine := fmt.Sprintf(
		"%d: %s: %s mtu %d state %s mode DEFAULT group default qlen 1000",
		iface.Index, iface.Name, flags, iface.MTU, state)

	var linkLine string
	if brdMAC != "" {
		linkLine = fmt.Sprintf("    link/%s %s brd %s", ltype, mac, brdMAC)
	} else {
		linkLine = fmt.Sprintf("    link/%s %s", ltype, mac)
	}

	if do.oneline {
		callCtx.Outf("%s\\%s\n", headerLine, linkLine)
		return
	}

	callCtx.Outf("%s\n%s\n", headerLine, linkLine)
}

// ---------------------------------------------------------------------------
// ip route implementation
// ---------------------------------------------------------------------------

// routeCmd dispatches ip route subcommands.
func routeCmd(ctx context.Context, callCtx *builtins.CallContext, do displayOpts, args []string) builtins.Result {
	if do.ipv6 {
		callCtx.Errf("ip: route: IPv6 routing not supported\n")
		return builtins.Result{Code: 1}
	}

	sub := "show"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}

	switch sub {
	case "show", "list":
		return routeShow(ctx, callCtx)
	case "get":
		if len(args) < 2 {
			callCtx.Errf("ip: route get: missing address argument\n")
			return builtins.Result{Code: 1}
		}
		return routeGet(ctx, callCtx, args[1])
	case "add", "del", "delete", "change", "replace", "flush", "save", "restore":
		callCtx.Errf("ip: route: %s: write operations are not permitted\n", sub)
		return builtins.Result{Code: 1}
	default:
		callCtx.Errf("ip: route: %s: unknown subcommand\n", sub)
		return builtins.Result{Code: 1}
	}
}

// routeShow prints the IPv4 routing table in ip-route(8) format.
func routeShow(ctx context.Context, callCtx *builtins.CallContext) builtins.Result {
	if runtime.GOOS != "linux" {
		callCtx.Errf("ip: route: not supported on %s\n", runtime.GOOS)
		return builtins.Result{Code: 1}
	}

	routes, err := parseRoutingTable(ctx, callCtx)
	if err != nil {
		callCtx.Errf("ip: route: %s\n", callCtx.PortableErr(err))
		return builtins.Result{Code: 1}
	}

	for i := range routes {
		if ctx.Err() != nil {
			break
		}
		callCtx.Outf("%s\n", formatRoute(&routes[i]))
	}
	return builtins.Result{}
}

// routeGet finds and prints the route used to reach addr.
func routeGet(ctx context.Context, callCtx *builtins.CallContext, addr string) builtins.Result {
	if runtime.GOOS != "linux" {
		callCtx.Errf("ip: route: not supported on %s\n", runtime.GOOS)
		return builtins.Result{Code: 1}
	}

	addrVal, ok := parseIPv4(addr)
	if !ok {
		callCtx.Errf("ip: route get: invalid address %q\n", addr)
		return builtins.Result{Code: 1}
	}

	routes, err := parseRoutingTable(ctx, callCtx)
	if err != nil {
		callCtx.Errf("ip: route: %s\n", callCtx.PortableErr(err))
		return builtins.Result{Code: 1}
	}

	best := longestPrefixMatch(routes, addrVal)
	if best == nil {
		callCtx.Errf("ip: route get: network unreachable\n")
		return builtins.Result{Code: 1}
	}

	var b strings.Builder
	b.WriteString(addr)
	if best.flags&rtfGateway != 0 {
		b.WriteString(" via ")
		b.WriteString(hexToIPStr(best.gw))
	}
	b.WriteString(" dev ")
	b.WriteString(best.iface)
	b.WriteByte('\n')
	callCtx.Out(b.String())
	return builtins.Result{}
}

// parseRoutingTable reads ProcNetRoutePath and returns UP route entries.
func parseRoutingTable(ctx context.Context, callCtx *builtins.CallContext) ([]routeEntry, error) {
	f, err := callCtx.OpenFile(ctx, ProcNetRoutePath, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	buf := make([]byte, routeScanBufInit)
	sc.Buffer(buf, MaxLineBytes)

	var routes []routeEntry
	firstLine := true
	for sc.Scan() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if firstLine {
			firstLine = false
			continue // skip header row
		}
		if len(routes) >= maxRoutes {
			break
		}
		r, ok := parseRouteEntry(sc.Text())
		if !ok {
			continue
		}
		if r.flags&rtfUp == 0 {
			continue // skip routes that are not UP
		}
		routes = append(routes, r)
	}
	return routes, sc.Err()
}

// parseRouteEntry parses a single data line from /proc/net/route.
// Fields are whitespace-separated; IP/flag/mask fields are hex, metric is decimal.
func parseRouteEntry(line string) (routeEntry, bool) {
	fields := strings.Fields(line)
	if len(fields) < 11 {
		return routeEntry{}, false
	}

	dest, err := strconv.ParseUint(fields[1], 16, 32)
	if err != nil {
		return routeEntry{}, false
	}
	gw, err := strconv.ParseUint(fields[2], 16, 32)
	if err != nil {
		return routeEntry{}, false
	}
	flags, err := strconv.ParseUint(fields[3], 16, 32)
	if err != nil {
		return routeEntry{}, false
	}
	metric, err := strconv.ParseUint(fields[6], 10, 32)
	if err != nil {
		return routeEntry{}, false
	}
	mask, err := strconv.ParseUint(fields[7], 16, 32)
	if err != nil {
		return routeEntry{}, false
	}

	return routeEntry{
		iface:  fields[0],
		dest:   uint32(dest),
		gw:     uint32(gw),
		flags:  uint32(flags),
		metric: uint32(metric),
		mask:   uint32(mask),
	}, true
}

// formatRoute returns the ip-route(8) display string for r.
func formatRoute(r *routeEntry) string {
	var b strings.Builder

	if r.dest == 0 {
		b.WriteString("default")
	} else {
		b.WriteString(hexToIPStr(r.dest))
		b.WriteByte('/')
		b.WriteString(strconv.Itoa(popcount(r.mask)))
	}

	if r.flags&rtfGateway != 0 {
		b.WriteString(" via ")
		b.WriteString(hexToIPStr(r.gw))
	}

	b.WriteString(" dev ")
	b.WriteString(r.iface)

	if r.metric != 0 {
		b.WriteString(" metric ")
		b.WriteString(strconv.Itoa(int(r.metric)))
	}

	return b.String()
}

// longestPrefixMatch returns the route that best matches addr,
// or nil if no route matches.
func longestPrefixMatch(routes []routeEntry, addr uint32) *routeEntry {
	var best *routeEntry
	bestBits := -1

	for i := range routes {
		r := &routes[i]
		if addr&r.mask == r.dest {
			bits := popcount(r.mask)
			if bits > bestBits {
				bestBits = bits
				best = r
			}
		}
	}
	return best
}

// hexToIPStr converts a /proc/net/route little-endian uint32 to dotted-decimal.
// The encoding stores the first octet in the least-significant byte:
// 192.168.1.1 is encoded as 0x0101A8C0, and hexToIPStr(0x0101A8C0) = "192.168.1.1".
func hexToIPStr(val uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d",
		val&0xFF,
		(val>>8)&0xFF,
		(val>>16)&0xFF,
		(val>>24)&0xFF,
	)
}

// parseIPv4 converts a dotted-decimal IPv4 string to the /proc/net/route
// little-endian uint32 encoding: first octet → lowest byte of the uint32.
func parseIPv4(s string) (uint32, bool) {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return 0, false
	}
	var val uint32
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 8)
		if err != nil {
			return 0, false
		}
		val |= uint32(n) << (uint(i) * 8)
	}
	return val, true
}

// popcount returns the number of set bits in v.
func popcount(v uint32) int {
	n := 0
	for v != 0 {
		n += int(v & 1)
		v >>= 1
	}
	return n
}
