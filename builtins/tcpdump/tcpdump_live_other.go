// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !linux && !darwin

package tcpdump

import (
	"context"
	"fmt"
)

// openLiveInterface is not supported on this platform.
func openLiveInterface(_ context.Context, iface string, _ int) (packetReader, error) {
	return nil, fmt.Errorf("live capture on interface %q is not supported on this platform", iface)
}
