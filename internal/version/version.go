// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package version exposes the build version of rshell.
//
// When rshell is imported as a library (e.g. by the Datadog Agent), Version
// is read from Go's embedded dependency info via [debug.ReadBuildInfo].
//
// For development builds, Version can be overridden via ldflags:
//
//	go build -ldflags "-X github.com/DataDog/rshell/internal/version.Version=v0.0.10-3-gabcdef1-dirty"
package version

import "runtime/debug"

const modulePath = "github.com/DataDog/rshell"

// Version is the build version string. Set via ldflags at build time for dev
// builds (e.g. "v0.0.10-3-gabcdef1-dirty"). When not set, falls back to the
// module version from build info.
var Version string

func init() {
	if Version == "" {
		Version = buildVersion()
	}
}

// Commit is the short git commit hash. Set via ldflags at build time.
var Commit string

// buildVersion reads the rshell version from Go's embedded build info.
// When rshell is a dependency (e.g. in the Datadog Agent), the version
// from go.mod is embedded automatically. For standalone builds it returns "dev".
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	// When built as a standalone binary, check the main module.
	if info.Main.Path == modulePath && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	// When imported as a library, find ourselves in the dependency list.
	for _, dep := range info.Deps {
		if dep.Path == modulePath {
			return dep.Version
		}
	}
	return "dev"
}
