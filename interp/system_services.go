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

// SystemServiceAction identifies an operation that may be granted for a
// system service.
type SystemServiceAction = builtins.SystemServiceAction

const (
	SystemServiceRead    = builtins.SystemServiceRead
	SystemServiceReload  = builtins.SystemServiceReload
	SystemServiceRestart = builtins.SystemServiceRestart
)

// SystemServiceControlGrant grants Actions for one exact Service spelling.
// Service names are never normalized, expanded, or resolved as aliases.
type SystemServiceControlGrant struct {
	Service string
	Actions []SystemServiceAction
}

type systemServiceGrants map[string]map[SystemServiceAction]struct{}

// AllowedSystemServices configures the system services and actions that
// system-service builtins may use. A grant matches its Service exactly: for
// example, "mysql" and "mysql.service" are different service names.
//
// Grants without actions are ignored. Empty service names and names containing
// whitespace, control characters, path separators, or glob patterns are
// skipped with a warning. Supported actions are read, reload, and restart;
// unsupported actions are skipped with a warning. Duplicate services and
// actions are accepted and combined idempotently.
//
// When not set (default), or when passed an empty slice, every system service
// is denied. This policy is not bypassed by allowing all commands.
func AllowedSystemServices(grants []SystemServiceControlGrant) RunnerOption {
	return func(r *Runner) error {
		allowed := make(systemServiceGrants, len(grants))
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
				if !validSystemServiceAction(action) {
					warning := fmt.Sprintf("AllowedSystemServices: skipping unsupported action %q in grant %d for %q\n", action, i, grant.Service)
					r.sandboxWarnings = append(r.sandboxWarnings, warning...)
					continue
				}
				if actions == nil {
					actions = make(map[SystemServiceAction]struct{}, len(grant.Actions))
					allowed[grant.Service] = actions
				}
				actions[action] = struct{}{}
			}
		}
		r.allowedSystemServices = allowed
		return nil
	}
}

func validSystemServiceAction(action SystemServiceAction) bool {
	switch action {
	case SystemServiceRead, SystemServiceReload, SystemServiceRestart:
		return true
	default:
		return false
	}
}

func validateSystemServiceName(service string) error {
	if service == "" {
		return fmt.Errorf("system service name must not be empty")
	}
	if strings.ContainsRune(service, '/') || strings.ContainsRune(service, '\\') {
		return fmt.Errorf("system service name %q must not contain a path separator", service)
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

func (r *Runner) authorizeSystemServices(action SystemServiceAction, services ...string) error {
	if !r.remediationMode {
		return fmt.Errorf("system service actions require remediation mode")
	}
	if !validSystemServiceAction(action) {
		return fmt.Errorf("unsupported system service action %q", action)
	}
	if len(services) == 0 {
		return fmt.Errorf("at least one system service is required")
	}

	for _, service := range services {
		if err := validateSystemServiceName(service); err != nil {
			return err
		}
		actions := r.allowedSystemServices[service]
		if _, ok := actions[action]; !ok {
			return fmt.Errorf("system service %q is not allowed for action %q", service, action)
		}
	}
	return nil
}
