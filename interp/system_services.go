// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/DataDog/rshell/builtins"
)

// SystemdAction identifies an operation that may be granted for a systemd
// service.
type SystemdAction = builtins.SystemdAction

const (
	SystemdActionRead    = builtins.SystemdActionRead
	SystemdActionClean   = builtins.SystemdActionClean
	SystemdActionReload  = builtins.SystemdActionReload
	SystemdActionRestart = builtins.SystemdActionRestart
)

// SystemdOperation is one service action checked by the shared policy.
type SystemdOperation = builtins.SystemdOperation

// Compatibility aliases for the original service-only policy.
type SystemServiceAction = builtins.SystemServiceAction

const (
	SystemServiceRead    = builtins.SystemServiceRead
	SystemServiceClean   = builtins.SystemServiceClean
	SystemServiceReload  = builtins.SystemServiceReload
	SystemServiceRestart = builtins.SystemServiceRestart
)

// SystemServiceControlGrant grants Actions for one exact Service.
type SystemServiceControlGrant struct {
	Service string
	Actions []SystemServiceAction
}

// SystemdControlGrant is an alias for the shared systemd policy grant type.
type SystemdControlGrant = SystemServiceControlGrant

type systemdGrants map[string]map[SystemdAction]struct{}

// AllowedSystemServices configures the services and actions that systemd-aware
// builtins may use. Service names are matched exactly: for example, "mysql"
// and "mysql.service" are different services.
//
// Grants without actions are ignored. Invalid services and unsupported actions
// are skipped with a warning. Supported actions are read, clean, reload, and
// restart. Duplicate services and actions are accepted and combined
// idempotently.
//
// When not set (default), or when passed an empty slice, every systemd
// operation is denied. This policy is not bypassed by allowing all commands.
func AllowedSystemServices(grants []SystemServiceControlGrant) RunnerOption {
	return func(r *Runner) error {
		allowed := make(systemdGrants, len(grants))
		for i, grant := range grants {
			if len(grant.Actions) == 0 {
				continue
			}
			if err := validateSystemServiceName(grant.Service); err != nil {
				warning := fmt.Sprintf("AllowedSystemServices: skipping grant %d: %v\n", i, err)
				r.sandboxWarnings = append(r.sandboxWarnings, warning...)
				continue
			}

			actions := allowed[grant.Service]
			for _, action := range grant.Actions {
				if !validSystemdAction(action) {
					warning := fmt.Sprintf("AllowedSystemServices: skipping unsupported action %q in grant %d for %q\n", action, i, grant.Service)
					r.sandboxWarnings = append(r.sandboxWarnings, warning...)
					continue
				}
				if actions == nil {
					actions = make(map[SystemdAction]struct{}, len(grant.Actions))
					allowed[grant.Service] = actions
				}
				actions[action] = struct{}{}
			}
		}
		r.allowedSystemServices = allowed
		return nil
	}
}

func validSystemdAction(action SystemdAction) bool {
	return action == SystemdActionRead ||
		action == SystemdActionClean ||
		action == SystemdActionReload ||
		action == SystemdActionRestart
}

func validateSystemServiceName(service string) error {
	if service == "" {
		return fmt.Errorf("system service name must not be empty")
	}
	if strings.ContainsRune(service, '/') || strings.ContainsRune(service, '\\') {
		return fmt.Errorf("system service name %q must not contain a path separator", service)
	}
	if strings.ContainsRune(service, ':') {
		return fmt.Errorf("system service name %q must not contain ':'", service)
	}
	for _, r := range service {
		switch r {
		case '*', '?', '[', ']':
			return fmt.Errorf("system service name %q must not contain a glob pattern", service)
		}
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("system service name %q must not contain whitespace or control characters", service)
		}
	}
	return nil
}

func (r *Runner) authorizeSystemd(operations ...SystemdOperation) error {
	if len(operations) == 0 {
		return fmt.Errorf("at least one systemd operation is required")
	}

	for _, operation := range operations {
		if err := validateSystemServiceName(operation.Service); err != nil {
			return err
		}
		if !validSystemdAction(operation.Action) {
			return fmt.Errorf("unsupported systemd action %q for system service %q", operation.Action, operation.Service)
		}
		if operation.Action != SystemdActionRead && !r.remediationMode {
			return fmt.Errorf("systemd action %q requires remediation mode", operation.Action)
		}
		actions := r.allowedSystemServices[operation.Service]
		if _, ok := actions[operation.Action]; !ok {
			return fmt.Errorf("system service %q is not allowed for action %q", operation.Service, operation.Action)
		}
	}
	return nil
}

func (r *Runner) authorizeSystemServices(action SystemServiceAction, services ...string) error {
	if len(services) == 0 {
		return fmt.Errorf("at least one system service is required")
	}
	operations := make([]SystemdOperation, len(services))
	for i, service := range services {
		operations[i] = SystemdOperation{Service: service, Action: action}
	}
	return r.authorizeSystemd(operations...)
}
