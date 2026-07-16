// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package systemd contains the trusted target and transport implementation
// used by systemd-aware builtins.
package systemd

import (
	"fmt"
	"path/filepath"
	"strings"
)

const MaxJournalDirs = 8

// Target identifies one systemd host. Root is accepted only as input to
// ResolveTarget; resolved targets retain it internally for rooted symlink
// resolution and expose explicit paths in the remaining fields.
type Target struct {
	Root                 string
	JournalDirs          []string
	MachineIDPath        string
	JournalControlSocket string
	SystemBusSocket      string
	root                 string
}

// LocalTarget returns the standard paths for the local systemd host.
func LocalTarget() Target {
	return targetFromRoot("/")
}

// ResolveTarget validates and resolves a target. A zero target selects the
// local host. Root derives every standard path below that root and cannot be
// mixed with explicit fields. Once any explicit field is supplied, omitted
// fields remain empty and never fall back to local paths.
func ResolveTarget(target Target) (Target, error) {
	if target.Root != "" {
		if len(target.JournalDirs) > 0 || target.MachineIDPath != "" || target.JournalControlSocket != "" || target.SystemBusSocket != "" {
			return Target{}, fmt.Errorf("systemd target root cannot be combined with explicit paths")
		}
		root, err := validateAbsolutePath("root", target.Root)
		if err != nil {
			return Target{}, err
		}
		resolved := targetFromRoot(root)
		resolved.root = root
		return resolved, nil
	}

	if len(target.JournalDirs) == 0 && target.MachineIDPath == "" && target.JournalControlSocket == "" && target.SystemBusSocket == "" {
		return LocalTarget(), nil
	}
	if len(target.JournalDirs) > MaxJournalDirs {
		return Target{}, fmt.Errorf("systemd target has %d journal directories; maximum is %d", len(target.JournalDirs), MaxJournalDirs)
	}
	if target.MachineIDPath == "" {
		return Target{}, fmt.Errorf("systemd target machine ID path is required with explicit paths")
	}

	resolved := Target{JournalDirs: make([]string, 0, len(target.JournalDirs))}
	seenDirs := make(map[string]struct{}, len(target.JournalDirs))
	for i, dir := range target.JournalDirs {
		clean, err := validateAbsolutePath(fmt.Sprintf("journal directory %d", i), dir)
		if err != nil {
			return Target{}, err
		}
		if _, exists := seenDirs[clean]; exists {
			continue
		}
		seenDirs[clean] = struct{}{}
		resolved.JournalDirs = append(resolved.JournalDirs, clean)
	}

	var err error
	if resolved.MachineIDPath, err = validateAbsolutePath("machine ID path", target.MachineIDPath); err != nil {
		return Target{}, err
	}
	if resolved.JournalControlSocket, err = validateOptionalAbsolutePath("journal control socket", target.JournalControlSocket); err != nil {
		return Target{}, err
	}
	if resolved.SystemBusSocket, err = validateOptionalAbsolutePath("system bus socket", target.SystemBusSocket); err != nil {
		return Target{}, err
	}
	return resolved, nil
}

func targetFromRoot(root string) Target {
	return Target{
		JournalDirs: []string{
			filepath.Join(root, "var", "log", "journal"),
			filepath.Join(root, "run", "log", "journal"),
		},
		MachineIDPath:        filepath.Join(root, "etc", "machine-id"),
		JournalControlSocket: filepath.Join(root, "run", "systemd", "journal", "io.systemd.journal"),
		SystemBusSocket:      filepath.Join(root, "run", "dbus", "system_bus_socket"),
	}
}

func validateOptionalAbsolutePath(name, path string) (string, error) {
	if path == "" {
		return "", nil
	}
	return validateAbsolutePath(name, path)
}

func validateAbsolutePath(name, path string) (string, error) {
	if strings.IndexByte(path, 0) >= 0 {
		return "", fmt.Errorf("systemd target %s contains a NUL byte", name)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("systemd target %s %q must be absolute", name, path)
	}
	return filepath.Clean(path), nil
}
