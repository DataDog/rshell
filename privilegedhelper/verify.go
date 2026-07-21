// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.

package privilegedhelper

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
)

const (
	remediationBundle = "com.datadoghq.remoteaction.rshell"
	remediationAction = "runRemediationCommand"
	EscalationAllowed = "EscalationAllowed"
)

type VerifiedCommand struct {
	TaskID             string
	Command            string
	AllowedCommands    []string
	AllowedPaths       []string
	ElevatableCommands []string
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
	if task.GetOrgId() != c.OrgID {
		return nil, errors.New("signed task orgId does not match helper credential")
	}
	if task.GetConnectionInfo().GetRunnerId() != c.RunnerID {
		return nil, errors.New("signed task runnerId does not match helper credential")
	}
	if task.GetBundleId() != remediationBundle || task.GetActionName() != remediationAction {
		return nil, errors.New("signed task is not an rshell remediation action")
	}
	inputs := task.GetInputs().AsMap()
	command, _ := inputs["command"].(string)
	if command == "" {
		return nil, errors.New("signed task command is required")
	}
	permissions, _ := inputs["effectivePermissions"].(string)
	if permissions != EscalationAllowed {
		return nil, errors.New("signed task does not allow selective elevation")
	}
	requestedElevatable, err := stringSlice(inputs["elevatableCommands"])
	if err != nil {
		return nil, fmt.Errorf("elevatableCommands: %w", err)
	}
	remote := task.GetSystemInputs().GetRemoteAction()
	if remote == nil {
		return nil, errors.New("signed task remote-action policy is required")
	}
	return &VerifiedCommand{
		TaskID: task.GetTaskId(), Command: command,
		AllowedCommands:    intersectCommands(remote.GetAllowedCommands(), c.AllowedCommands),
		AllowedPaths:       intersectPaths(remote.GetAllowedPaths(), c.AllowedPaths),
		ElevatableCommands: intersectExact(requestedElevatable, c.ElevatableCommands),
	}, nil
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
			if !slices.Contains(result, narrower) {
				result = append(result, narrower)
			}
		}
	}
	return result
}

func stringSlice(value any) ([]string, error) {
	values, ok := value.([]any)
	if !ok {
		return nil, errors.New("must be an array")
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		s, ok := value.(string)
		if !ok || s == "" {
			return nil, errors.New("must contain non-empty strings")
		}
		result = append(result, s)
	}
	return result, nil
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
