//go:build windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

// devFDSupported indicates whether /dev/fd/N is available for process
// substitution. Windows does not support /dev/fd.
const devFDSupported = false
