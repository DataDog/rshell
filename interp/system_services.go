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
// resource.
type SystemdAction = builtins.SystemdAction

const (
	SystemdActionRead    = builtins.SystemdActionRead
	SystemdActionClean   = builtins.SystemdActionClean
	SystemdActionReload  = builtins.SystemdActionReload
	SystemdActionRestart = builtins.SystemdActionRestart
)

// SystemdResource identifies an exact resource in the shared systemd policy.
type SystemdResource = builtins.SystemdResource

const (
	SystemdResourceJournalAll     = builtins.SystemdResourceJournalAll
	SystemdResourceJournalKernel  = builtins.SystemdResourceJournalKernel
	SystemdResourceJournalStorage = builtins.SystemdResourceJournalStorage
	SystemdResourceManager        = builtins.SystemdResourceManager
)

// SystemdUnitResource returns the policy resource for one exact unit name.
func SystemdUnitResource(name string) SystemdResource {
	return builtins.SystemdUnitResource(name)
}

// SystemdOperation is one resource/action pair checked by the shared policy.
type SystemdOperation = builtins.SystemdOperation

// Compatibility aliases for the original service-only policy.
type SystemServiceAction = builtins.SystemServiceAction

const (
	SystemServiceRead    = builtins.SystemServiceRead
	SystemServiceReload  = builtins.SystemServiceReload
	SystemServiceRestart = builtins.SystemServiceRestart
)

// SystemServiceControlGrant grants Actions for one exact systemd resource.
// Service is shorthand for an exact unit resource; callers must set exactly
// one of Resource or Service.
type SystemServiceControlGrant struct {
	Service  string
	Actions  []SystemServiceAction
	Resource SystemdResource
}

// SystemdControlGrant is a resource-oriented alias for the original grant
// type used by AllowedSystemServices.
type SystemdControlGrant = SystemServiceControlGrant

type systemdGrants map[SystemdResource]map[SystemdAction]struct{}

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

func systemdGrantResource(grant SystemServiceControlGrant) (SystemdResource, error) {
	switch {
	case grant.Resource != "" && grant.Service != "":
		return "", fmt.Errorf("must not set both Resource and Service")
	case grant.Resource != "":
		return grant.Resource, nil
	default:
		return SystemdUnitResource(grant.Service), nil
	}
}

func validSystemdOperation(operation SystemdOperation) bool {
	switch {
	case strings.HasPrefix(string(operation.Resource), "unit:"):
		return operation.Action == SystemdActionRead || operation.Action == SystemdActionReload || operation.Action == SystemdActionRestart
	case operation.Resource == SystemdResourceJournalAll,
		operation.Resource == SystemdResourceJournalKernel:
		return operation.Action == SystemdActionRead
	case operation.Resource == SystemdResourceJournalStorage:
		return operation.Action == SystemdActionRead || operation.Action == SystemdActionClean
	case operation.Resource == SystemdResourceManager:
		return operation.Action == SystemdActionRead || operation.Action == SystemdActionReload
	default:
		return false
	}
}

func validateSystemdResource(resource SystemdResource) error {
	const unitPrefix = "unit:"
	resourceName := string(resource)
	if strings.HasPrefix(resourceName, unitPrefix) {
		return validateSystemServiceName(resourceName[len(unitPrefix):])
	}
	switch resource {
	case SystemdResourceJournalAll, SystemdResourceJournalKernel, SystemdResourceJournalStorage, SystemdResourceManager:
		return nil
	default:
		return fmt.Errorf("unsupported systemd resource %q", resource)
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

func (r *Runner) authorizeSystemd(operations ...SystemdOperation) error {
	if len(operations) == 0 {
		return fmt.Errorf("at least one systemd operation is required")
	}

	for _, operation := range operations {
		if err := validateSystemdResource(operation.Resource); err != nil {
			return err
		}
		if !validSystemdOperation(operation) {
			return fmt.Errorf("unsupported systemd operation %q on %q", operation.Action, operation.Resource)
		}
		if operation.Action != SystemdActionRead && !r.remediationMode {
			return fmt.Errorf("systemd action %q requires remediation mode", operation.Action)
		}
		actions := r.allowedSystemServices[operation.Resource]
		if _, ok := actions[operation.Action]; !ok {
			return fmt.Errorf("systemd resource %q is not allowed for action %q", operation.Resource, operation.Action)
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
		operations[i] = SystemdOperation{Resource: SystemdUnitResource(service), Action: action}
	}
	return r.authorizeSystemd(operations...)
}
