// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithJournalVacuumPolicyCopiesValidatedPolicy(t *testing.T) {
	policy := JournalVacuumPolicy{
		MinRetentionAge:  24 * time.Hour,
		MinRetainedFiles: 2,
		MinRetainedBytes: 64 * 1024 * 1024,
		MaxDeletedFiles:  4,
		MaxDeletedBytes:  128 * 1024 * 1024,
	}
	runner, err := New(WithJournalVacuumPolicy(policy))
	require.NoError(t, err)
	defer runner.Close()

	policy.MinRetainedFiles = 0
	require.NotNil(t, runner.journalVacuumPolicy)
	assert.Equal(t, 2, runner.journalVacuumPolicy.MinRetainedFiles)
}

func TestWithJournalVacuumPolicyIsDisabledByDefault(t *testing.T) {
	runner, err := New()
	require.NoError(t, err)
	defer runner.Close()
	assert.Nil(t, runner.journalVacuumPolicy)
}

func TestWithJournalVacuumPolicyRejectsUnboundedPolicies(t *testing.T) {
	valid := JournalVacuumPolicy{
		MinRetentionAge: time.Hour,
		MaxDeletedFiles: 1,
		MaxDeletedBytes: 1,
	}
	tests := []struct {
		name   string
		mutate func(*JournalVacuumPolicy)
		needle string
	}{
		{name: "no retention age", mutate: func(p *JournalVacuumPolicy) { p.MinRetentionAge = 0 }, needle: "retention age"},
		{name: "negative retained files", mutate: func(p *JournalVacuumPolicy) { p.MinRetainedFiles = -1 }, needle: "retained files"},
		{name: "no file ceiling", mutate: func(p *JournalVacuumPolicy) { p.MaxDeletedFiles = 0 }, needle: "deleted files"},
		{name: "no byte ceiling", mutate: func(p *JournalVacuumPolicy) { p.MaxDeletedBytes = 0 }, needle: "deleted bytes"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := valid
			test.mutate(&policy)
			runner, err := New(WithJournalVacuumPolicy(policy))
			if runner != nil {
				runner.Close()
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.needle)
		})
	}
}
