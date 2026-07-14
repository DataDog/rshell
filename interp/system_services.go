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
// service or fixed capability.
type SystemdAction = builtins.SystemdAction

const (
	SystemdActionRead    = builtins.SystemdActionRead
	SystemdActionClean   = builtins.SystemdActionClean
	SystemdActionReload  = builtins.SystemdActionReload
	SystemdActionRestart = builtins.SystemdActionRestart
)

// SystemdResource identifies a fixed non-service capability in the shared
// systemd policy.
type SystemdResource = builtins.SystemdResource

const (
	SystemdResourceJournalAll     = builtins.SystemdResourceJournalAll
	SystemdResourceJournalKernel  = builtins.SystemdResourceJournalKernel
	SystemdResourceJournalStorage = builtins.SystemdResourceJournalStorage
	SystemdResourceManager        = builtins.SystemdResourceManager
)

// SystemdOperation is one service or fixed-resource action checked by the
// shared policy.
type SystemdOperation = builtins.SystemdOperation

// Compatibility aliases for the original service-only policy.
type SystemServiceAction = builtins.SystemServiceAction

const (
	SystemServiceRead    = builtins.SystemServiceRead
	SystemServiceReload  = builtins.SystemServiceReload
	SystemServiceRestart = builtins.SystemServiceRestart
)

// SystemServiceControlGrant grants Actions for one exact Service or one fixed
// non-service Resource. Callers must set exactly one of Service or Resource.
type SystemServiceControlGrant struct {
	Service  string
	Actions  []SystemServiceAction
	Resource SystemdResource
}

// SystemdControlGrant is an alias for the shared systemd policy grant type.
type SystemdControlGrant = SystemServiceControlGrant

type systemdGrantTarget struct {
	service  string
	resource SystemdResource
}

type systemdGrants map[systemdGrantTarget]map[SystemdAction]struct{}

// AllowedSystemServices configures the services, fixed resources, and actions
// that systemd-aware builtins may use. Service names are matched exactly: for
// example, "mysql" and "mysql.service" are different services.
//
// Grants without actions are ignored. Invalid targets and unsupported
// target/action pairs are skipped with a warning. Supported fixed resources
// are journal:all, journal:kernel, journal:storage, and manager. Supported
// actions are read, clean, reload, and restart. Duplicate targets and actions
// are accepted and combined idempotently.
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
			target, err := makeSystemdGrantTarget(grant.Service, grant.Resource)
			if err != nil {
				warning := fmt.Sprintf("AllowedSystemServices: skipping grant %d: %v\n", i, err)
				r.sandboxWarnings = append(r.sandboxWarnings, warning...)
				continue
			}

			actions := allowed[target]
			for _, action := range grant.Actions {
				if !validSystemdOperation(target, action) {
					warning := fmt.Sprintf("AllowedSystemServices: skipping unsupported action %q in grant %d for %q\n", action, i, target.selector())
					r.sandboxWarnings = append(r.sandboxWarnings, warning...)
					continue
				}
				if actions == nil {
					actions = make(map[SystemdAction]struct{}, len(grant.Actions))
					allowed[target] = actions
				}
				actions[action] = struct{}{}
			}
		}
		r.allowedSystemServices = allowed
		return nil
	}
}

func makeSystemdGrantTarget(service string, resource SystemdResource) (systemdGrantTarget, error) {
	switch {
	case resource != "" && service != "":
		return systemdGrantTarget{}, fmt.Errorf("must not set both Resource and Service")
	case service != "":
		if err := validateSystemServiceName(service); err != nil {
			return systemdGrantTarget{}, err
		}
		return systemdGrantTarget{service: service}, nil
	case resource != "":
		if err := validateSystemdResource(resource); err != nil {
			return systemdGrantTarget{}, err
		}
		return systemdGrantTarget{resource: resource}, nil
	default:
		return systemdGrantTarget{}, validateSystemServiceName("")
	}
}

func (target systemdGrantTarget) selector() string {
	if target.service != "" {
		return target.service
	}
	return string(target.resource)
}

func (target systemdGrantTarget) description() string {
	if target.service != "" {
		return fmt.Sprintf("system service %q", target.service)
	}
	return fmt.Sprintf("systemd resource %q", target.resource)
}

func validSystemdOperation(target systemdGrantTarget, action SystemdAction) bool {
	switch {
	case target.service != "":
		return action == SystemdActionRead || action == SystemdActionReload || action == SystemdActionRestart
	case target.resource == SystemdResourceJournalAll,
		target.resource == SystemdResourceJournalKernel:
		return action == SystemdActionRead
	case target.resource == SystemdResourceJournalStorage:
		return action == SystemdActionRead || action == SystemdActionClean
	case target.resource == SystemdResourceManager:
		return action == SystemdActionRead || action == SystemdActionReload
	default:
		return false
	}
}

func validateSystemdResource(resource SystemdResource) error {
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
		target, err := makeSystemdGrantTarget(operation.Service, operation.Resource)
		if err != nil {
			return err
		}
		if !validSystemdOperation(target, operation.Action) {
			return fmt.Errorf("unsupported systemd action %q for %s", operation.Action, target.description())
		}
		if operation.Action != SystemdActionRead && !r.remediationMode {
			return fmt.Errorf("systemd action %q requires remediation mode", operation.Action)
		}
		actions := r.allowedSystemServices[target]
		if _, ok := actions[operation.Action]; !ok {
			return fmt.Errorf("%s is not allowed for action %q", target.description(), operation.Action)
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
