// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package procnetsocket

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVulnHuntSubsystemSSProcnetReaders_RejectsTraversalProcPath(t *testing.T) {
	readers := map[string]func(context.Context, string) ([]SocketEntry, error){
		"tcp4": ReadTCP4,
		"tcp6": ReadTCP6,
		"udp4": ReadUDP4,
		"udp6": ReadUDP6,
		"unix": ReadUnix,
	}
	for name, reader := range readers {
		t.Run(name, func(t *testing.T) {
			_, err := reader(context.Background(), "/proc/../tmp")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unsafe procPath")
		})
	}
}
