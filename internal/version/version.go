// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package version exposes the build version of rshell.
//
// The source constant [version] is the single source of truth for the release
// version. It must be updated before tagging a new release.
//
// For development builds, Version can be overridden via ldflags to include
// git metadata (commit offset, dirty state, etc.):
//
//	go build -ldflags "-X github.com/DataDog/rshell/internal/version.Version=v0.0.10-3-gabcdef1-dirty"
//
// When not overridden, Version defaults to the source constant, which is
// correct for both direct builds and library consumers who import rshell.
package version

// version is the release version. This constant is the single source of truth
// and must match the git tag at release time. Update this before tagging.
const version = "0.0.10"

// Version is the build version string. Defaults to the source constant.
// Overridden via ldflags at build time for dev builds (e.g. "0.0.10-3-gabcdef1-dirty").
var Version = version

// Commit is the short git commit hash. Set via ldflags at build time.
var Commit string
