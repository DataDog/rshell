// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins"
)

func TestLoadProgramRejectsTooManyFilesBeforeOpening(t *testing.T) {
	opened := 0
	callCtx := &builtins.CallContext{
		OpenFile: func(context.Context, string, int, os.FileMode) (io.ReadWriteCloser, error) {
			opened++
			return &closeTrackedFile{Reader: strings.NewReader("")}, nil
		},
	}
	programFiles := make([]string, MaxProgramFiles+1)

	_, _, err := loadProgram(context.Background(), callCtx, nil, programFiles)

	require.EqualError(t, err, fmt.Sprintf("too many program files (maximum %d)", MaxProgramFiles))
	assert.Zero(t, opened)
}

func TestLoadProgramCountsFileSeparatorsTowardSizeLimit(t *testing.T) {
	tests := []struct {
		name        string
		firstSize   int
		wantErr     bool
		wantOpened  int
		wantProgram int
	}{
		{name: "at limit", firstSize: MaxProgramBytes - 1, wantOpened: 2, wantProgram: MaxProgramBytes},
		{name: "over limit", firstSize: MaxProgramBytes, wantErr: true, wantOpened: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opened := 0
			contents := map[string]string{
				"first.awk": strings.Repeat(" ", tt.firstSize),
				"empty.awk": "",
			}
			callCtx := &builtins.CallContext{
				OpenFile: func(_ context.Context, path string, _ int, _ os.FileMode) (io.ReadWriteCloser, error) {
					opened++
					return &closeTrackedFile{Reader: strings.NewReader(contents[path])}, nil
				},
			}

			program, _, err := loadProgram(context.Background(), callCtx, nil, []string{"first.awk", "empty.awk"})

			if tt.wantErr {
				require.EqualError(t, err, fmt.Sprintf("program exceeds %d bytes", MaxProgramBytes))
			} else {
				require.NoError(t, err)
				assert.Len(t, program, tt.wantProgram)
			}
			assert.Equal(t, tt.wantOpened, opened)
		})
	}
}
