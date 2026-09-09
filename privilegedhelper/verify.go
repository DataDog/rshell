// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package privilegedhelper

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	rshellBundle      = "com.datadoghq.remoteaction.rshell"
	readOnlyAction    = "runCommand"
	remediationAction = "runRemediationCommand"
	EscalationAllowed = "EscalationAllowed"
)

type effectivePermissions string

const effectivePermissionsEscalationAllowed effectivePermissions = EscalationAllowed

// ExecutionMode is derived exclusively from the authenticated action name and
// carried to the one-shot worker as part of the verified command policy.
type ExecutionMode string

const (
	ExecutionModeReadOnly    ExecutionMode = "readonly"
	ExecutionModeRemediation ExecutionMode = "remediation"
)

// signedRunCommandInputs is decoded only from PrivateActionTask.inputs after
// the containing envelope's signature has been verified. Do not populate this
// type from fields on the outer socket request: those would not be covered by
// the backend signature.
type signedRunCommandInputs struct {
	Command            string
	Permissions        effectivePermissions
	ElevatableCommands []string
}

type VerifiedCommand struct {
	TaskID                string
	Command               string
	Mode                  ExecutionMode
	AllowedCommands       []string
	AllowedPaths          []string
	AllowedSystemServices []SystemServiceGrant
	ElevatableCommands    []string
	authorization         authorizationContext
}

type SystemServiceGrant struct {
	Service string   `json:"service"`
	Actions []string `json:"actions"`
}

type authorizationPolicy struct {
	AllowedCommands       []string             `json:"allowedCommands"`
	AllowedPaths          []string             `json:"allowedPaths"`
	AllowedSystemServices []SystemServiceGrant `json:"allowedSystemServices"`
	ElevatableCommands    []string             `json:"elevatableCommands"`
}

type authorizationContext struct {
	TaskID               string               `json:"taskId"`
	OrgID                int64                `json:"orgId"`
	RunnerID             string               `json:"runnerId"`
	BundleID             string               `json:"bundleId"`
	ActionName           string               `json:"actionName"`
	EffectivePermissions effectivePermissions `json:"effectivePermissions"`
	ExpirationTime       time.Time            `json:"expirationTime"`
	TrustedKeyCount      int                  `json:"trustedKeyCount"`
	Signed               authorizationPolicy  `json:"signed"`
	Local                authorizationPolicy  `json:"local"`
	Effective            authorizationPolicy  `json:"effective"`
}

func (c *Credential) Verify(req ExecuteRequest, now time.Time) (*VerifiedCommand, error) {
	if req.Version != ProtocolVersion {
		return nil, fmt.Errorf("unsupported request version %d", req.Version)
	}
	if err := c.verifyEnvelope(req.Envelope); err != nil {
		return nil, err
	}
	var task PrivateActionTask
	if err := proto.Unmarshal(req.Envelope.Data, &task); err != nil {
		return nil, fmt.Errorf("decode signed task: %w", err)
	}
	if task.GetExpirationTime() == nil || !task.GetExpirationTime().IsValid() {
		return nil, errors.New("signed task expiration is missing or invalid")
	}
	if !task.GetExpirationTime().AsTime().After(now) {
		return nil, errors.New("signed task is expired")
	}
	if !c.trustBackendPolicy {
		if c.OrgID > 0 && task.GetOrgId() != c.OrgID {
			return nil, errors.New("signed task orgId does not match helper credential")
		}
		if c.RunnerID != "" && task.GetConnectionInfo().GetRunnerId() != c.RunnerID {
			return nil, errors.New("signed task runnerId does not match helper credential")
		}
	}
	if task.GetBundleId() != rshellBundle {
		return nil, errors.New("signed task is not an rshell action")
	}
	var mode ExecutionMode
	switch task.GetActionName() {
	case readOnlyAction:
		mode = ExecutionModeReadOnly
	case remediationAction:
		mode = ExecutionModeRemediation
	default:
		return nil, errors.New("signed task is not a supported rshell action")
	}
	inputs, err := decodeSignedRunCommandInputs(task.GetInputs())
	if err != nil {
		return nil, err
	}
	if inputs.Permissions != effectivePermissionsEscalationAllowed {
		return nil, errors.New("signed task does not allow selective elevation")
	}
	remote := task.GetSystemInputs().GetRemoteAction()
	if remote == nil {
		return nil, errors.New("signed task remote-action policy is required")
	}
	effectiveAllowedCommands := slices.Clone(remote.GetAllowedCommands())
	effectiveAllowedPaths := slices.Clone(remote.GetAllowedPaths())
	signedAllowedSystemServices := signedSystemServiceGrants(remote.GetSystemServices())
	localAllowedSystemServices := configuredSystemServiceGrants(c.AllowedSystemServices)
	effectiveAllowedSystemServices := cloneSystemServiceGrants(signedAllowedSystemServices)
	effectiveElevatableCommands := slices.Clone(inputs.ElevatableCommands)
	if !c.trustBackendPolicy {
		effectiveAllowedCommands = intersectCommands(remote.GetAllowedCommands(), c.AllowedCommands)
		effectiveAllowedPaths = intersectPaths(remote.GetAllowedPaths(), c.AllowedPaths)
		effectiveAllowedSystemServices = intersectSystemServiceGrants(signedAllowedSystemServices, localAllowedSystemServices)
		effectiveElevatableCommands = intersectExact(inputs.ElevatableCommands, c.ElevatableCommands)
	}
	return &VerifiedCommand{
		TaskID: task.GetTaskId(), Command: inputs.Command, Mode: mode,
		AllowedCommands:       effectiveAllowedCommands,
		AllowedPaths:          effectiveAllowedPaths,
		AllowedSystemServices: effectiveAllowedSystemServices,
		ElevatableCommands:    effectiveElevatableCommands,
		authorization: authorizationContext{
			TaskID:               task.GetTaskId(),
			OrgID:                task.GetOrgId(),
			RunnerID:             task.GetConnectionInfo().GetRunnerId(),
			BundleID:             task.GetBundleId(),
			ActionName:           task.GetActionName(),
			EffectivePermissions: inputs.Permissions,
			ExpirationTime:       task.GetExpirationTime().AsTime(),
			TrustedKeyCount:      len(c.decodedKeys),
			Signed: authorizationPolicy{
				AllowedCommands:       slices.Clone(remote.GetAllowedCommands()),
				AllowedPaths:          slices.Clone(remote.GetAllowedPaths()),
				AllowedSystemServices: cloneSystemServiceGrants(signedAllowedSystemServices),
				ElevatableCommands:    slices.Clone(inputs.ElevatableCommands),
			},
			Local: authorizationPolicy{
				AllowedCommands:       slices.Clone(c.AllowedCommands),
				AllowedPaths:          slices.Clone(c.AllowedPaths),
				AllowedSystemServices: cloneSystemServiceGrants(localAllowedSystemServices),
				ElevatableCommands:    slices.Clone(c.ElevatableCommands),
			},
			Effective: authorizationPolicy{
				AllowedCommands:       slices.Clone(effectiveAllowedCommands),
				AllowedPaths:          slices.Clone(effectiveAllowedPaths),
				AllowedSystemServices: cloneSystemServiceGrants(effectiveAllowedSystemServices),
				ElevatableCommands:    slices.Clone(effectiveElevatableCommands),
			},
		},
	}, nil
}

func decodeSignedRunCommandInputs(inputs *structpb.Struct) (signedRunCommandInputs, error) {
	var result signedRunCommandInputs
	if inputs == nil {
		return result, errors.New("signed task inputs are required")
	}
	command, err := requiredStringField(inputs, "command")
	if err != nil {
		return result, err
	}
	permissions, err := requiredStringField(inputs, "effectivePermissions")
	if err != nil {
		return result, err
	}
	elevatable, err := stringListField(inputs, "elevatableCommands")
	if err != nil {
		return result, err
	}
	result.Command = command
	result.Permissions = effectivePermissions(permissions)
	result.ElevatableCommands = elevatable
	return result, nil
}

func requiredStringField(inputs *structpb.Struct, name string) (string, error) {
	field := inputs.GetFields()[name]
	if field == nil {
		return "", fmt.Errorf("signed task %s is required", name)
	}
	value, ok := field.GetKind().(*structpb.Value_StringValue)
	if !ok || value.StringValue == "" {
		return "", fmt.Errorf("signed task %s must be a non-empty string", name)
	}
	return value.StringValue, nil
}

func stringListField(inputs *structpb.Struct, name string) ([]string, error) {
	field := inputs.GetFields()[name]
	if field == nil {
		return nil, fmt.Errorf("signed task %s is required", name)
	}
	list, ok := field.GetKind().(*structpb.Value_ListValue)
	if !ok {
		return nil, fmt.Errorf("signed task %s must be an array", name)
	}
	result := make([]string, 0, len(list.ListValue.GetValues()))
	for _, item := range list.ListValue.GetValues() {
		value, ok := item.GetKind().(*structpb.Value_StringValue)
		if !ok || value.StringValue == "" {
			return nil, fmt.Errorf("signed task %s must contain non-empty strings", name)
		}
		result = append(result, value.StringValue)
	}
	return result, nil
}

func intersectCommands(requested, configured []string) []string {
	if slices.Contains(configured, "rshell:*") {
		result := make([]string, 0, len(requested))
		for _, value := range requested {
			if strings.HasPrefix(value, "rshell:") && value != "rshell:" && !slices.Contains(result, value) {
				result = append(result, value)
			}
		}
		return result
	}
	return intersectExact(requested, configured)
}

type pathPolicy struct {
	value     string
	readWrite bool
}

func parsePathPolicy(value string) pathPolicy {
	policy := pathPolicy{value: value}
	if strings.HasSuffix(value, ":rw") {
		policy.value = strings.TrimSuffix(value, ":rw")
		policy.readWrite = true
	}
	if strings.HasSuffix(value, ":ro") {
		policy.value = strings.TrimSuffix(value, ":ro")
	}
	policy.value = path.Clean(policy.value)
	return policy
}

func containsPath(root, candidate string) bool {
	return root == "/" || candidate == root || strings.HasPrefix(candidate, root+"/")
}

func intersectPaths(requested, configured []string) []string {
	result := make([]string, 0, len(requested))
	for _, requestedValue := range requested {
		req := parsePathPolicy(requestedValue)
		if !path.IsAbs(req.value) {
			continue
		}
		for _, configuredValue := range configured {
			cfg := parsePathPolicy(configuredValue)
			if !path.IsAbs(cfg.value) {
				continue
			}
			var narrower string
			switch {
			case containsPath(cfg.value, req.value):
				narrower = req.value
			case containsPath(req.value, cfg.value):
				narrower = cfg.value
			default:
				continue
			}
			if req.readWrite && cfg.readWrite {
				narrower += ":rw"
			}
			narrowerPolicy := parsePathPolicy(narrower)
			existing := -1
			for i, value := range result {
				if parsePathPolicy(value).value == narrowerPolicy.value {
					existing = i
					break
				}
			}
			if existing == -1 {
				result = append(result, narrower)
			} else if narrowerPolicy.readWrite {
				// Each candidate is already the intersection of one signed
				// and one local grant. If any candidate grants read-write,
				// retain it when collapsing duplicate normalized paths.
				// This prevents an earlier read-only duplicate from
				// shadowing the mutually authorized write grant.
				result[existing] = narrower
			}
		}
	}
	return result
}

func intersectExact(requested, configured []string) []string {
	result := make([]string, 0, len(requested))
	for _, value := range requested {
		if slices.Contains(configured, value) && !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	return result
}

func signedSystemServiceGrants(services map[string]*structpb.ListValue) []SystemServiceGrant {
	raw := make(map[string][]string, len(services))
	for service, values := range services {
		actions := make([]string, 0, len(values.GetValues()))
		for _, value := range values.GetValues() {
			if action, ok := value.GetKind().(*structpb.Value_StringValue); ok {
				actions = append(actions, action.StringValue)
			}
		}
		raw[service] = actions
	}
	return configuredSystemServiceGrants(raw)
}

func configuredSystemServiceGrants(services map[string][]string) []SystemServiceGrant {
	names := make([]string, 0, len(services))
	for service := range services {
		names = append(names, service)
	}
	sort.Strings(names)

	grants := make([]SystemServiceGrant, 0, len(names))
	for _, service := range names {
		actions := slices.Clone(services[service])
		sort.Strings(actions)
		actions = slices.Compact(actions)
		if len(actions) == 0 {
			continue
		}
		grants = append(grants, SystemServiceGrant{Service: service, Actions: actions})
	}
	return grants
}

func intersectSystemServiceGrants(requested, configured []SystemServiceGrant) []SystemServiceGrant {
	configuredByService := make(map[string][]string, len(configured))
	for _, grant := range configured {
		configuredByService[grant.Service] = grant.Actions
	}

	result := make([]SystemServiceGrant, 0, len(requested))
	for _, grant := range requested {
		configuredActions, ok := configuredByService[grant.Service]
		if !ok {
			continue
		}
		actions := intersectSystemServiceActions(grant.Actions, configuredActions)
		if len(actions) > 0 {
			result = append(result, SystemServiceGrant{Service: grant.Service, Actions: actions})
		}
	}
	return result
}

func intersectSystemServiceActions(requested, configured []string) []string {
	if slices.Contains(configured, "*") {
		return slices.Clone(requested)
	}
	if slices.Contains(requested, "*") {
		return slices.Clone(configured)
	}
	return intersectExact(requested, configured)
}

func cloneSystemServiceGrants(grants []SystemServiceGrant) []SystemServiceGrant {
	cloned := make([]SystemServiceGrant, len(grants))
	for i, grant := range grants {
		cloned[i] = SystemServiceGrant{Service: grant.Service, Actions: slices.Clone(grant.Actions)}
	}
	return cloned
}
