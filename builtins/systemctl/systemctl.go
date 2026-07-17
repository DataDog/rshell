// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package systemctl implements a capability-bounded systemd unit manager.
//
// Unlike the host systemctl binary, this builtin accepts only exact unit names.
// Enumeration is restricted to units with an explicit read grant, inspection
// exposes a fixed set of fields, and every mutation is authorized before the
// trusted systemd backend is called.
package systemctl

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/DataDog/rshell/builtins"
)

const (
	maxFilterValues     = 32
	maxFilterValueBytes = 64
	maxPropertyValues   = 32
)

var supportedUnitTypes = map[string]struct{}{
	"automount": {},
	"device":    {},
	"mount":     {},
	"path":      {},
	"scope":     {},
	"service":   {},
	"slice":     {},
	"socket":    {},
	"swap":      {},
	"target":    {},
	"timer":     {},
}

var showPropertyNames = []string{
	"Id",
	"Description",
	"LoadState",
	"ActiveState",
	"SubState",
	"UnitFileState",
	"MainPID",
	"Result",
	"ExecMainCode",
	"ExecMainStatus",
}

var showPropertySet = func() map[string]struct{} {
	properties := make(map[string]struct{}, len(showPropertyNames))
	for _, property := range showPropertyNames {
		properties[property] = struct{}{}
	}
	return properties
}()

// Cmd is the systemctl builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "systemctl",
	Description: "inspect and control explicitly authorized systemd units",
	MakeFlags:   makeFlags,
}

type flags struct {
	all        *bool
	unitTypes  *[]string
	states     *[]string
	noLegend   *bool
	properties *[]string
	value      *bool
	quiet      *bool
	now        *bool
	help       *bool
}

func makeFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	_ = fs.Bool("system", false, "operate on the configured system manager (accepted for compatibility)")
	_ = fs.Bool("no-pager", false, "do not invoke a pager (accepted for compatibility)")
	options := flags{
		all:        fs.BoolP("all", "a", false, "include inactive read-authorized units in list-units"),
		unitTypes:  fs.StringArrayP("type", "t", nil, "list only unit TYPE (repeatable or comma-separated)"),
		states:     fs.StringArray("state", nil, "list only unit STATE (repeatable or comma-separated)"),
		noLegend:   fs.Bool("no-legend", false, "omit the list-units header and restriction summary"),
		properties: fs.StringArrayP("property", "p", nil, "show only fixed PROPERTY (repeatable or comma-separated)"),
		value:      fs.Bool("value", false, "with show, print property values only"),
		quiet:      fs.BoolP("quiet", "q", false, "suppress is-* state output"),
		now:        fs.Bool("now", false, "start after enable or stop after disable"),
		help:       fs.BoolP("help", "h", false, "print usage and exit"),
	}
	return options.run(fs)
}

func (options flags) run(fs *builtins.FlagSet) builtins.HandlerFunc {
	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *options.help {
			printHelp(callCtx, fs)
			return builtins.Result{}
		}

		verb := "list-units"
		operands := args
		if len(args) > 0 {
			verb = args[0]
			operands = args[1:]
		}

		switch verb {
		case "list-units":
			if result, ok := rejectFlags(callCtx, fs, verb, "all", "type", "state", "no-legend"); !ok {
				return result
			}
			return options.runList(ctx, callCtx, operands)
		case "status":
			if result, ok := rejectFlags(callCtx, fs, verb); !ok {
				return result
			}
			return runStatus(ctx, callCtx, operands)
		case "show":
			if result, ok := rejectFlags(callCtx, fs, verb, "property", "value"); !ok {
				return result
			}
			return options.runShow(ctx, callCtx, operands)
		case "is-active", "is-failed":
			if result, ok := rejectFlags(callCtx, fs, verb, "quiet"); !ok {
				return result
			}
			return options.runIsState(ctx, callCtx, verb, operands)
		case "is-enabled":
			if result, ok := rejectFlags(callCtx, fs, verb, "quiet"); !ok {
				return result
			}
			return options.runIsEnabled(ctx, callCtx, operands)
		case "start":
			return runJobWithFlags(ctx, callCtx, fs, verb, operands, builtins.SystemServiceJobStart, builtins.SystemServiceStart)
		case "stop":
			return runJobWithFlags(ctx, callCtx, fs, verb, operands, builtins.SystemServiceJobStop, builtins.SystemServiceStop)
		case "reload":
			return runJobWithFlags(ctx, callCtx, fs, verb, operands, builtins.SystemServiceJobReload, builtins.SystemServiceReload)
		case "restart":
			return runJobWithFlags(ctx, callCtx, fs, verb, operands, builtins.SystemServiceJobRestart, builtins.SystemServiceRestart)
		case "try-restart":
			return runJobWithFlags(ctx, callCtx, fs, verb, operands, builtins.SystemServiceJobTryRestart, builtins.SystemServiceRestart)
		case "reload-or-restart":
			return runJobWithFlags(ctx, callCtx, fs, verb, operands, builtins.SystemServiceJobReloadOrRestart, builtins.SystemServiceReload, builtins.SystemServiceRestart)
		case "try-reload-or-restart":
			return runJobWithFlags(ctx, callCtx, fs, verb, operands, builtins.SystemServiceJobTryReloadOrRestart, builtins.SystemServiceReload, builtins.SystemServiceRestart)
		case "reset-failed":
			if result, ok := rejectFlags(callCtx, fs, verb); !ok {
				return result
			}
			return runResetFailed(ctx, callCtx, operands)
		case "enable", "disable":
			if result, ok := rejectFlags(callCtx, fs, verb, "now"); !ok {
				return result
			}
			return options.runEnableDisable(ctx, callCtx, verb, operands)
		default:
			callCtx.Errf("systemctl: unsupported command %q\n", safeText(verb))
			callCtx.Errf("Try 'systemctl --help' for more information.\n")
			return builtins.Result{Code: 1}
		}
	}
}

func printHelp(callCtx *builtins.CallContext, fs *builtins.FlagSet) {
	callCtx.Out("Usage: systemctl [OPTION]... COMMAND [UNIT]...\n")
	callCtx.Out("Inspect and control exact systemd units through bounded capabilities.\n")
	callCtx.Out("Bare systemctl is equivalent to list-units. list-units can see only\n")
	callCtx.Out("the exact units granted read access; it never enumerates the whole host.\n\n")
	callCtx.Out("Commands:\n")
	callCtx.Out("  list-units                 List read-authorized units\n")
	callCtx.Out("  status UNIT...             Show bounded unit status without logs\n")
	callCtx.Out("  show UNIT...               Show a fixed safe property set\n")
	callCtx.Out("  is-active UNIT...          Test whether any unit is active\n")
	callCtx.Out("  is-failed UNIT...          Test whether any unit is failed\n")
	callCtx.Out("  is-enabled UNIT...         Test whether any unit is enabled\n")
	callCtx.Out("  start|stop|reload UNIT...  Queue and wait for an authorized job\n")
	callCtx.Out("  restart|try-restart UNIT...\n")
	callCtx.Out("  reload-or-restart|try-reload-or-restart UNIT...\n")
	callCtx.Out("  reset-failed UNIT...       Clear failed state\n")
	callCtx.Out("  enable|disable UNIT...     Change unit-file state; supports --now\n\n")
	callCtx.Out("Systemd dependencies and install metadata may affect additional units.\n")
	callCtx.Out("enable/disable also reload the whole configured manager.\n\n")
	callCtx.Out("Options are accepted only by the commands described in their help text:\n")
	fs.SetOutput(callCtx.Stdout)
	fs.PrintDefaults()
}

func rejectFlags(callCtx *builtins.CallContext, fs *builtins.FlagSet, verb string, allowed ...string) (builtins.Result, bool) {
	allowedSet := make(map[string]struct{}, len(allowed)+3)
	allowedSet["help"] = struct{}{}
	allowedSet["system"] = struct{}{}
	allowedSet["no-pager"] = struct{}{}
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}

	var rejected string
	fs.Visit(func(flag *builtins.Flag) {
		if rejected != "" {
			return
		}
		if _, ok := allowedSet[flag.Name]; !ok {
			rejected = flag.Name
		}
	})
	if rejected == "" {
		return builtins.Result{}, true
	}
	callCtx.Errf("systemctl: --%s is not supported with %s\n", rejected, verb)
	return builtins.Result{Code: 1}, false
}

func (options flags) runList(ctx context.Context, callCtx *builtins.CallContext, operands []string) builtins.Result {
	if len(operands) != 0 {
		callCtx.Errf("systemctl: list-units does not accept unit operands\n")
		return builtins.Result{Code: 1}
	}
	unitTypes, err := parseFilterValues(*options.unitTypes, "type", true)
	if err != nil {
		return commandError(callCtx, err)
	}
	states, err := parseFilterValues(*options.states, "state", false)
	if err != nil {
		return commandError(callCtx, err)
	}
	if callCtx.ReadableSystemServices == nil {
		return commandError(callCtx, fmt.Errorf("readable systemd unit capability is not available"))
	}
	units := readableSystemctlUnits(callCtx.ReadableSystemServices(), unitTypes)
	if len(units) > builtins.MaxSystemServiceOperands {
		return commandError(callCtx, fmt.Errorf("too many readable units (maximum %d)", builtins.MaxSystemServiceOperands))
	}
	slices.SortFunc(units, func(left, right string) int {
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
		return 0
	})
	if len(units) == 0 {
		writeUnitList(callCtx, nil, *options.noLegend)
		return builtins.Result{}
	}
	if result := authorize(callCtx, units, builtins.SystemServiceRead); result.Code != 0 {
		return result
	}
	if callCtx.Systemd == nil || callCtx.Systemd.ServiceState == nil {
		return commandError(callCtx, fmt.Errorf("systemd unit state capability is not available"))
	}

	listed, err := callCtx.Systemd.ServiceState.ListSystemServices(ctx, builtins.SystemServiceListRequest{
		Services:        append([]string(nil), units...),
		IncludeInactive: *options.all,
	})
	if err != nil {
		return backendError(ctx, callCtx, err)
	}
	listed, err = validateListedStates(units, listed)
	if err != nil {
		return commandError(callCtx, err)
	}
	listed = filterStates(listed, unitTypes, states)
	writeUnitList(callCtx, listed, *options.noLegend)
	return builtins.Result{}
}

func runStatus(ctx context.Context, callCtx *builtins.CallContext, operands []string) builtins.Result {
	units, err := validateUnits(operands, false)
	if err != nil {
		return commandError(callCtx, err)
	}
	states, result := inspectAuthorized(ctx, callCtx, units)
	if result.Code != 0 {
		return result
	}

	code := uint8(0)
	for i, state := range states {
		if i > 0 {
			callCtx.Out("\n")
		}
		if state.Description == "" {
			callCtx.Outf("%s\n", state.Name)
		} else {
			callCtx.Outf("%s - %s\n", state.Name, state.Description)
		}
		callCtx.Outf("     Loaded: %s", displayToken(state.LoadState))
		if state.UnitFileState != "" {
			callCtx.Outf(" (%s)", state.UnitFileState)
		}
		callCtx.Out("\n")
		callCtx.Outf("     Active: %s", displayToken(state.ActiveState))
		if state.SubState != "" {
			callCtx.Outf(" (%s)", state.SubState)
		}
		callCtx.Out("\n")
		if state.MainPID != 0 {
			callCtx.Outf("   Main PID: %d\n", state.MainPID)
		}
		if state.Result != "" {
			callCtx.Outf("     Result: %s\n", state.Result)
		}

		if state.LoadState == "not-found" {
			code = 4
		} else if code < 3 && !activeStateSuccess(state.ActiveState) {
			code = 3
		}
	}
	return builtins.Result{Code: code}
}

func (options flags) runShow(ctx context.Context, callCtx *builtins.CallContext, operands []string) builtins.Result {
	properties, err := parseProperties(*options.properties)
	if err != nil {
		return commandError(callCtx, err)
	}
	units, err := validateUnits(operands, false)
	if err != nil {
		return commandError(callCtx, err)
	}
	states, result := inspectAuthorized(ctx, callCtx, units)
	if result.Code != 0 {
		return result
	}

	for i, state := range states {
		if i > 0 {
			callCtx.Out("\n")
		}
		for _, property := range properties {
			value := propertyValue(state, property)
			if *options.value {
				callCtx.Outf("%s\n", value)
			} else {
				callCtx.Outf("%s=%s\n", property, value)
			}
		}
	}
	return builtins.Result{}
}

func (options flags) runIsState(ctx context.Context, callCtx *builtins.CallContext, verb string, operands []string) builtins.Result {
	units, err := validateUnits(operands, false)
	if err != nil {
		return commandError(callCtx, err)
	}
	states, result := inspectAuthorized(ctx, callCtx, units)
	if result.Code != 0 {
		return result
	}

	success := false
	allNotFound := true
	for _, state := range states {
		if !*options.quiet {
			callCtx.Outf("%s\n", displayToken(state.ActiveState))
		}
		if verb == "is-active" && activeStateSuccess(state.ActiveState) {
			success = true
		}
		if verb == "is-failed" && state.ActiveState == "failed" {
			success = true
		}
		if state.LoadState != "not-found" {
			allNotFound = false
		}
	}
	if success {
		return builtins.Result{}
	}
	if allNotFound {
		return builtins.Result{Code: 4}
	}
	if verb == "is-active" {
		return builtins.Result{Code: 3}
	}
	return builtins.Result{Code: 1}
}

func (options flags) runIsEnabled(ctx context.Context, callCtx *builtins.CallContext, operands []string) builtins.Result {
	units, err := validateUnits(operands, false)
	if err != nil {
		return commandError(callCtx, err)
	}
	if result := authorize(callCtx, units, builtins.SystemServiceRead); result.Code != 0 {
		return result
	}
	if callCtx.Systemd == nil || callCtx.Systemd.ServiceState == nil {
		return commandError(callCtx, fmt.Errorf("systemd unit state capability is not available"))
	}
	states, err := callCtx.Systemd.ServiceState.SystemServiceEnabledState(ctx, append([]string(nil), units...))
	if err != nil {
		return backendError(ctx, callCtx, err)
	}
	if len(states) != len(units) {
		return commandError(callCtx, fmt.Errorf("systemd manager returned %d enabled states for %d units", len(states), len(units)))
	}

	success := false
	allNotFound := true
	for _, state := range states {
		if err := validateStateToken("unit file state", state); err != nil {
			return commandError(callCtx, err)
		}
		if !*options.quiet {
			callCtx.Outf("%s\n", displayToken(state))
		}
		if enabledStateSuccess(state) {
			success = true
		}
		if state != "not-found" {
			allNotFound = false
		}
	}
	if success {
		return builtins.Result{}
	}
	if allNotFound {
		return builtins.Result{Code: 4}
	}
	return builtins.Result{Code: 1}
}

func runJobWithFlags(ctx context.Context, callCtx *builtins.CallContext, fs *builtins.FlagSet, verb string, operands []string, job builtins.SystemServiceJobAction, actions ...builtins.SystemServiceAction) builtins.Result {
	if result, ok := rejectFlags(callCtx, fs, verb); !ok {
		return result
	}
	units, err := validateUnits(operands, false)
	if err != nil {
		return commandError(callCtx, err)
	}
	if result := authorize(callCtx, units, actions...); result.Code != 0 {
		return result
	}
	if callCtx.Systemd == nil || callCtx.Systemd.ServiceControl == nil {
		return commandError(callCtx, fmt.Errorf("systemd unit control capability is not available"))
	}
	if err := callCtx.Systemd.ServiceControl.RunSystemServiceJobs(ctx, job, append([]string(nil), units...)); err != nil {
		return backendError(ctx, callCtx, err)
	}
	return builtins.Result{}
}

func runResetFailed(ctx context.Context, callCtx *builtins.CallContext, operands []string) builtins.Result {
	units, err := validateUnits(operands, false)
	if err != nil {
		return commandError(callCtx, err)
	}
	if result := authorize(callCtx, units, builtins.SystemServiceResetFailed); result.Code != 0 {
		return result
	}
	if callCtx.Systemd == nil || callCtx.Systemd.ServiceControl == nil {
		return commandError(callCtx, fmt.Errorf("systemd unit control capability is not available"))
	}
	if err := callCtx.Systemd.ServiceControl.ResetFailedSystemServices(ctx, append([]string(nil), units...)); err != nil {
		return backendError(ctx, callCtx, err)
	}
	return builtins.Result{}
}

func (options flags) runEnableDisable(ctx context.Context, callCtx *builtins.CallContext, verb string, operands []string) builtins.Result {
	units, err := validateUnits(operands, false)
	if err != nil {
		return commandError(callCtx, err)
	}
	if *options.now {
		for _, unit := range units {
			if isTemplateUnit(unit) {
				return commandError(callCtx, fmt.Errorf("--now is not supported for template unit %q; use an exact instance", unit))
			}
		}
	}
	actions := []builtins.SystemServiceAction{builtins.SystemServiceEnable}
	nowAction := builtins.SystemServiceStart
	nowJob := builtins.SystemServiceJobStart
	if verb == "disable" {
		actions[0] = builtins.SystemServiceDisable
		nowAction = builtins.SystemServiceStop
		nowJob = builtins.SystemServiceJobStop
	}
	if *options.now {
		actions = append(actions, nowAction)
	}
	if result := authorize(callCtx, units, actions...); result.Code != 0 {
		return result
	}
	if callCtx.Systemd == nil || callCtx.Systemd.ServiceControl == nil {
		return commandError(callCtx, fmt.Errorf("systemd unit control capability is not available"))
	}
	controller := callCtx.Systemd.ServiceControl
	if verb == "enable" {
		err = controller.EnableSystemServices(ctx, append([]string(nil), units...))
	} else {
		err = controller.DisableSystemServices(ctx, append([]string(nil), units...))
	}
	if err != nil {
		return backendError(ctx, callCtx, err)
	}
	if *options.now {
		if err := controller.RunSystemServiceJobs(ctx, nowJob, append([]string(nil), units...)); err != nil {
			return backendError(ctx, callCtx, fmt.Errorf("%s completed, but the --now %s job failed; unit-file changes were not rolled back: %w", verb, nowJob, err))
		}
	}
	return builtins.Result{}
}

func inspectAuthorized(ctx context.Context, callCtx *builtins.CallContext, units []string) ([]builtins.SystemServiceState, builtins.Result) {
	if result := authorize(callCtx, units, builtins.SystemServiceRead); result.Code != 0 {
		return nil, result
	}
	if callCtx.Systemd == nil || callCtx.Systemd.ServiceState == nil {
		return nil, commandError(callCtx, fmt.Errorf("systemd unit state capability is not available"))
	}
	states, err := callCtx.Systemd.ServiceState.InspectSystemServices(ctx, append([]string(nil), units...))
	if err != nil {
		return nil, backendError(ctx, callCtx, err)
	}
	states, err = validateInspectedStates(units, states)
	if err != nil {
		return nil, commandError(callCtx, err)
	}
	return states, builtins.Result{}
}

func authorize(callCtx *builtins.CallContext, units []string, actions ...builtins.SystemServiceAction) builtins.Result {
	if callCtx.AuthorizeSystemd == nil {
		return commandError(callCtx, fmt.Errorf("systemd authorization capability is not available"))
	}
	operations := make([]builtins.SystemdOperation, 0, len(units)*len(actions))
	for _, unit := range units {
		for _, action := range actions {
			operations = append(operations, builtins.SystemdOperation{Service: unit, Action: action})
		}
	}
	if err := callCtx.AuthorizeSystemd(operations...); err != nil {
		return commandError(callCtx, err)
	}
	return builtins.Result{}
}

func validateUnits(raw []string, allowEmpty bool) ([]string, error) {
	if len(raw) == 0 && !allowEmpty {
		return nil, fmt.Errorf("at least one exact unit is required")
	}
	if len(raw) > builtins.MaxSystemServiceOperands {
		return nil, fmt.Errorf("too many unit operands (maximum %d)", builtins.MaxSystemServiceOperands)
	}
	units := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, unit := range raw {
		if err := validateUnitName(unit); err != nil {
			return nil, err
		}
		if _, exists := seen[unit]; exists {
			continue
		}
		seen[unit] = struct{}{}
		units = append(units, unit)
	}
	return units, nil
}

func readableSystemctlUnits(raw []string, unitTypes map[string]struct{}) []string {
	capacity := len(raw)
	if capacity > builtins.MaxSystemServiceOperands+1 {
		capacity = builtins.MaxSystemServiceOperands + 1
	}
	units := make([]string, 0, capacity)
	seen := make(map[string]struct{}, capacity)
	for _, unit := range raw {
		// The shared policy also serves journalctl, whose exact selectors may be
		// legacy service names without a unit suffix. They remain valid grants but
		// are not candidates for systemctl enumeration.
		if validateUnitName(unit) != nil {
			continue
		}
		if len(unitTypes) > 0 {
			if _, ok := unitTypes[unitTypeOf(unit)]; !ok {
				continue
			}
		}
		if _, exists := seen[unit]; exists {
			continue
		}
		seen[unit] = struct{}{}
		units = append(units, unit)
		if len(units) > builtins.MaxSystemServiceOperands {
			return units
		}
	}
	return units
}

func validateUnitName(unit string) error {
	if unit == "" {
		return fmt.Errorf("unit name must not be empty")
	}
	if len(unit) > builtins.MaxSystemServiceNameBytes {
		return fmt.Errorf("unit name %q exceeds %d bytes", safeText(unit), builtins.MaxSystemServiceNameBytes)
	}
	if !utf8.ValidString(unit) {
		return fmt.Errorf("unit name contains invalid UTF-8")
	}
	dot := strings.LastIndexByte(unit, '.')
	if dot <= 0 || dot == len(unit)-1 {
		return fmt.Errorf("invalid exact unit name %q", safeText(unit))
	}
	unitType := unit[dot+1:]
	if _, ok := supportedUnitTypes[unitType]; !ok {
		return fmt.Errorf("unsupported unit type %q in %q", safeText(unitType), safeText(unit))
	}
	base := unit[:dot]
	atCount := 0
	for i := 0; i < len(base); i++ {
		char := base[i]
		if char == '@' {
			atCount++
			if i == 0 || atCount > 1 {
				return fmt.Errorf("invalid exact unit name %q", safeText(unit))
			}
			continue
		}
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return fmt.Errorf("invalid character in exact unit name %q", safeText(unit))
	}
	return nil
}

func validateCanonicalUnitName(unit string) error {
	if unit == "" || len(unit) > builtins.MaxSystemServiceNameBytes || !utf8.ValidString(unit) {
		return fmt.Errorf("invalid canonical unit name")
	}
	dot := strings.LastIndexByte(unit, '.')
	if dot <= 0 || dot == len(unit)-1 {
		return fmt.Errorf("invalid canonical unit name %q", safeText(unit))
	}
	if _, ok := supportedUnitTypes[unit[dot+1:]]; !ok {
		return fmt.Errorf("unsupported canonical unit type in %q", safeText(unit))
	}

	base := unit[:dot]
	atCount := 0
	for i := 0; i < len(base); i++ {
		char := base[i]
		if char == '\\' {
			if i+3 >= len(base) || base[i+1] != 'x' || !isHex(base[i+2]) || !isHex(base[i+3]) {
				return fmt.Errorf("invalid escape in canonical unit name %q", safeText(unit))
			}
			i += 3
			continue
		}
		if char == '@' {
			atCount++
			if i == 0 || atCount > 1 {
				return fmt.Errorf("invalid canonical unit name %q", safeText(unit))
			}
			continue
		}
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' || char == ':' {
			continue
		}
		return fmt.Errorf("invalid character in canonical unit name %q", safeText(unit))
	}
	return nil
}

func isHex(char byte) bool {
	return (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')
}

func parseFilterValues(raw []string, name string, unitType bool) (map[string]struct{}, error) {
	if len(raw) > maxFilterValues {
		return nil, fmt.Errorf("too many --%s values (maximum %d)", name, maxFilterValues)
	}
	values := make(map[string]struct{})
	total := 0
	for _, item := range raw {
		for value := range strings.SplitSeq(item, ",") {
			total++
			if total > maxFilterValues {
				return nil, fmt.Errorf("too many --%s values (maximum %d)", name, maxFilterValues)
			}
			if value == "" || len(value) > maxFilterValueBytes || !validStateToken(value) {
				return nil, fmt.Errorf("invalid --%s value %q", name, safeText(value))
			}
			if unitType {
				if _, ok := supportedUnitTypes[value]; !ok {
					return nil, fmt.Errorf("unsupported --type value %q", safeText(value))
				}
			}
			values[value] = struct{}{}
		}
	}
	return values, nil
}

func parseProperties(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return append([]string(nil), showPropertyNames...), nil
	}
	if len(raw) > maxPropertyValues {
		return nil, fmt.Errorf("too many properties (maximum %d)", maxPropertyValues)
	}
	properties := make([]string, 0, len(showPropertyNames))
	seen := make(map[string]struct{}, len(showPropertyNames))
	total := 0
	for _, item := range raw {
		for property := range strings.SplitSeq(item, ",") {
			total++
			if total > maxPropertyValues {
				return nil, fmt.Errorf("too many properties (maximum %d)", maxPropertyValues)
			}
			if _, ok := showPropertySet[property]; !ok {
				return nil, fmt.Errorf("unsupported property %q", safeText(property))
			}
			if _, exists := seen[property]; exists {
				continue
			}
			seen[property] = struct{}{}
			properties = append(properties, property)
		}
	}
	return properties, nil
}

func validateListedStates(units []string, states []builtins.SystemServiceState) ([]builtins.SystemServiceState, error) {
	allowed := make(map[string]struct{}, len(units))
	for _, unit := range units {
		allowed[unit] = struct{}{}
	}
	seen := make(map[string]struct{}, len(states))
	validated := make([]builtins.SystemServiceState, 0, len(states))
	for _, state := range states {
		if _, ok := allowed[state.Name]; !ok {
			return nil, fmt.Errorf("systemd manager returned unauthorized unit %q", safeText(state.Name))
		}
		if _, exists := seen[state.Name]; exists {
			return nil, fmt.Errorf("systemd manager returned duplicate unit %q", safeText(state.Name))
		}
		seen[state.Name] = struct{}{}
		state, err := validateBackendState(state)
		if err != nil {
			return nil, err
		}
		validated = append(validated, state)
	}
	slices.SortFunc(validated, func(left, right builtins.SystemServiceState) int {
		if left.Name < right.Name {
			return -1
		}
		if left.Name > right.Name {
			return 1
		}
		return 0
	})
	return validated, nil
}

func validateInspectedStates(units []string, states []builtins.SystemServiceState) ([]builtins.SystemServiceState, error) {
	if len(states) != len(units) {
		return nil, fmt.Errorf("systemd manager returned %d states for %d units", len(states), len(units))
	}
	validated := make([]builtins.SystemServiceState, len(states))
	for i, state := range states {
		if state.Name != units[i] {
			return nil, fmt.Errorf("systemd manager returned unit %q for requested unit %q", safeText(state.Name), units[i])
		}
		var err error
		validated[i], err = validateBackendState(state)
		if err != nil {
			return nil, err
		}
	}
	return validated, nil
}

func validateBackendState(state builtins.SystemServiceState) (builtins.SystemServiceState, error) {
	if err := validateUnitName(state.Name); err != nil {
		return state, fmt.Errorf("systemd manager returned invalid unit selector: %w", err)
	}
	if state.CanonicalName != "" {
		if err := validateCanonicalUnitName(state.CanonicalName); err != nil {
			return state, fmt.Errorf("systemd manager returned invalid canonical unit: %w", err)
		}
	}
	fields := []struct {
		name  string
		value *string
		token bool
	}{
		{name: "description", value: &state.Description},
		{name: "load state", value: &state.LoadState, token: true},
		{name: "active state", value: &state.ActiveState, token: true},
		{name: "sub state", value: &state.SubState, token: true},
		{name: "unit file state", value: &state.UnitFileState, token: true},
		{name: "result", value: &state.Result, token: true},
	}
	for _, field := range fields {
		if len(*field.value) > builtins.MaxSystemServiceFieldBytes {
			return state, fmt.Errorf("systemd manager %s exceeds %d bytes", field.name, builtins.MaxSystemServiceFieldBytes)
		}
		if field.token {
			if err := validateStateToken(field.name, *field.value); err != nil {
				return state, err
			}
		} else {
			*field.value = safeText(*field.value)
		}
	}
	return state, nil
}

func validateStateToken(name, value string) error {
	if len(value) > builtins.MaxSystemServiceFieldBytes {
		return fmt.Errorf("systemd manager %s exceeds %d bytes", name, builtins.MaxSystemServiceFieldBytes)
	}
	if value != "" && !validStateToken(value) {
		return fmt.Errorf("systemd manager returned invalid %s %q", name, safeText(value))
	}
	return nil
}

func validStateToken(value string) bool {
	for i := 0; i < len(value); i++ {
		char := value[i]
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func filterStates(states []builtins.SystemServiceState, unitTypes, wantedStates map[string]struct{}) []builtins.SystemServiceState {
	filtered := make([]builtins.SystemServiceState, 0, len(states))
	for _, state := range states {
		if len(unitTypes) > 0 {
			if _, ok := unitTypes[unitTypeOf(state.Name)]; !ok {
				continue
			}
		}
		if len(wantedStates) > 0 {
			_, loadMatch := wantedStates[state.LoadState]
			_, activeMatch := wantedStates[state.ActiveState]
			_, subMatch := wantedStates[state.SubState]
			if !loadMatch && !activeMatch && !subMatch {
				continue
			}
		}
		filtered = append(filtered, state)
	}
	return filtered
}

func writeUnitList(callCtx *builtins.CallContext, states []builtins.SystemServiceState, noLegend bool) {
	if !noLegend {
		callCtx.Out("UNIT LOAD ACTIVE SUB DESCRIPTION\n")
	}
	for _, state := range states {
		callCtx.Outf("%s %s %s %s %s\n",
			state.Name,
			displayToken(state.LoadState),
			displayToken(state.ActiveState),
			displayToken(state.SubState),
			state.Description,
		)
	}
	if !noLegend {
		callCtx.Outf("%d units listed (restricted to units granted read access).\n", len(states))
	}
}

func propertyValue(state builtins.SystemServiceState, property string) string {
	switch property {
	case "Id":
		if state.CanonicalName != "" {
			return state.CanonicalName
		}
		return state.Name
	case "Description":
		return state.Description
	case "LoadState":
		return state.LoadState
	case "ActiveState":
		return state.ActiveState
	case "SubState":
		return state.SubState
	case "UnitFileState":
		return state.UnitFileState
	case "MainPID":
		return strconv.FormatUint(uint64(state.MainPID), 10)
	case "Result":
		return state.Result
	case "ExecMainCode":
		return strconv.FormatInt(int64(state.ExecMainCode), 10)
	case "ExecMainStatus":
		return strconv.FormatInt(int64(state.ExecMainStatus), 10)
	default:
		return ""
	}
}

func unitTypeOf(unit string) string {
	dot := strings.LastIndexByte(unit, '.')
	if dot < 0 || dot == len(unit)-1 {
		return ""
	}
	return unit[dot+1:]
}

func isTemplateUnit(unit string) bool {
	dot := strings.LastIndexByte(unit, '.')
	return dot > 0 && unit[dot-1] == '@'
}

func activeStateSuccess(state string) bool {
	return state == "active" || state == "reloading" || state == "refreshing"
}

func enabledStateSuccess(state string) bool {
	return state == "enabled" || state == "enabled-runtime" || state == "static" || state == "alias" || state == "indirect" || state == "generated"
}

func displayToken(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func backendError(_ context.Context, callCtx *builtins.CallContext, err error) builtins.Result {
	// Manager mutations can complete, partially complete, or remain in flight
	// after the caller's context is canceled. Always preserve the backend's
	// outcome warning; the runner may additionally surface its generic timeout.
	callCtx.Errf("systemctl: %s\n", safeText(err.Error()))
	return builtins.Result{Code: 1}
}

func commandError(callCtx *builtins.CallContext, err error) builtins.Result {
	callCtx.Errf("systemctl: %s\n", safeText(err.Error()))
	return builtins.Result{Code: 1}
}

func safeText(value string) string {
	value = strings.ToValidUTF8(value, "?")
	return strings.Map(func(r rune) rune {
		if r == ' ' || unicode.IsGraphic(r) {
			return r
		}
		return '?'
	}, value)
}
