// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllowedSystemServicesAuthorizesExactServiceAndAction(t *testing.T) {
	runner, err := New(
		WithMode(ModeRemediation),
		AllowedSystemServices([]SystemServiceControlGrant{
			{
				Service: "mysql.service",
				Actions: []SystemServiceAction{
					SystemServiceRestart,
					SystemServiceReload,
					SystemServiceRead,
					SystemServiceClean,
				},
			},
			{
				Service: "nginx.service",
				Actions: []SystemServiceAction{SystemServiceRead},
			},
		}),
	)
	require.NoError(t, err)
	defer runner.Close()

	require.NoError(t, runner.authorizeSystemServices(SystemServiceRestart, "mysql.service"))
	require.NoError(t, runner.authorizeSystemServices(SystemServiceReload, "mysql.service"))
	require.NoError(t, runner.authorizeSystemServices(SystemServiceClean, "mysql.service"))
	require.NoError(t, runner.authorizeSystemServices(SystemServiceRead, "mysql.service", "nginx.service"))

	err = runner.authorizeSystemServices(SystemServiceRestart, "nginx.service")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `system service "nginx.service" is not allowed for action "restart"`)

	for _, service := range []string{"mysql", "MYSQL.service"} {
		err = runner.authorizeSystemServices(SystemServiceRead, service)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `system service "`+service+`" is not allowed`)
	}
}

func TestAllowedSystemServicesDefaultDenyIsIndependentOfAllowedCommands(t *testing.T) {
	runner, err := New(WithMode(ModeRemediation), allowAllCommandsOpt())
	require.NoError(t, err)
	defer runner.Close()

	err = runner.authorizeSystemServices(SystemServiceRead, "mysql.service")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
}

func TestAllowedSystemServicesAllowsReadOutsideRemediationMode(t *testing.T) {
	runner, err := New(AllowedSystemServices([]SystemServiceControlGrant{
		{
			Service: "mysql.service",
			Actions: []SystemServiceAction{SystemServiceRead, SystemServiceRestart, SystemServiceClean},
		},
	}))
	require.NoError(t, err)
	defer runner.Close()

	require.NoError(t, runner.authorizeSystemServices(SystemServiceRead, "mysql.service"))
	err = runner.authorizeSystemServices(SystemServiceRestart, "mysql.service")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `action "restart" requires remediation mode`)

	err = runner.authorizeSystemServices(SystemServiceClean, "mysql.service")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `action "clean" requires remediation mode`)
}

func TestAllowedSystemServicesReadDoesNotEnableMutation(t *testing.T) {
	runner, err := New(AllowedSystemServices([]SystemdControlGrant{
		{Service: "systemd-journald.service", Actions: []SystemdAction{SystemdActionRead}},
	}))
	require.NoError(t, err)
	defer runner.Close()

	require.NoError(t, runner.authorizeSystemd(
		SystemdOperation{Service: "systemd-journald.service", Action: SystemdActionRead},
	))
	err = runner.authorizeSystemd(
		SystemdOperation{Service: "systemd-journald.service", Action: SystemdActionClean},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `action "clean" requires remediation mode`)
}

func TestAllowedSystemServicesCopiesAndCombinesGrants(t *testing.T) {
	actions := []SystemServiceAction{SystemServiceRead}
	grants := []SystemServiceControlGrant{
		{Service: "mysql", Actions: actions},
		{Service: "mysql", Actions: []SystemServiceAction{SystemServiceRestart}},
	}
	runner, err := New(WithMode(ModeRemediation), AllowedSystemServices(grants))
	require.NoError(t, err)
	defer runner.Close()

	grants[0].Service = "changed"
	actions[0] = SystemServiceReload

	require.NoError(t, runner.authorizeSystemServices(SystemServiceRead, "mysql"))
	require.NoError(t, runner.authorizeSystemServices(SystemServiceRestart, "mysql"))
}

func TestAllowedSystemServicesSkipsEmptyAndInvalidGrants(t *testing.T) {
	var warningOutput bytes.Buffer
	missingPath := filepath.Join(t.TempDir(), "missing")
	runner, err := New(
		WithMode(ModeRemediation),
		WarningsWriter(&warningOutput),
		AllowedSystemServices([]SystemServiceControlGrant{
			{Service: "ignored.service"},
			{Service: "mysql.service", Actions: []SystemServiceAction{SystemServiceRead}},
			{Service: "", Actions: []SystemServiceAction{SystemServiceRead}},
			{Service: "mysql service", Actions: []SystemServiceAction{SystemServiceRead}},
			{Service: "mysql\u00a0service", Actions: []SystemServiceAction{SystemServiceRead}},
			{Service: "/etc/systemd/system/mysql.service", Actions: []SystemServiceAction{SystemServiceRead}},
			{Service: "C:\\svc", Actions: []SystemServiceAction{SystemServiceRead}},
			{Service: "..\\svc", Actions: []SystemServiceAction{SystemServiceRead}},
			{Service: "tenant:mysql.service", Actions: []SystemServiceAction{SystemServiceRead}},
			{Service: "mysql*.service", Actions: []SystemServiceAction{SystemServiceRead}},
		}),
		// Applying AllowedPaths after AllowedSystemServices verifies that one
		// allowlist option does not overwrite another option's warnings.
		AllowedPaths([]string{missingPath}),
	)
	require.NoError(t, err)
	defer runner.Close()

	require.NoError(t, runner.authorizeSystemServices(SystemServiceRead, "mysql.service"))
	assert.Len(t, runner.allowedSystemServices, 1)
	assert.NotContains(t, runner.allowedSystemServices, "ignored.service")

	warnings := runner.Warnings()
	require.Len(t, warnings, 9)
	for _, needle := range []string{
		"AllowedSystemServices: skipping grant 2: system service name must not be empty",
		"whitespace or control characters",
		`AllowedSystemServices: skipping grant 6: system service name "C:\\svc" must not contain a path separator`,
		`AllowedSystemServices: skipping grant 7: system service name "..\\svc" must not contain a path separator`,
		"path separator",
		"must not contain ':'",
		"glob pattern",
		"AllowedPaths: skipping",
	} {
		assert.Contains(t, warningOutput.String(), needle)
	}
	assert.NotContains(t, warningOutput.String(), "ignored.service")
}

func TestAllowedSystemServicesSkipsUnsupportedActions(t *testing.T) {
	var warningOutput bytes.Buffer
	runner, err := New(
		WithMode(ModeRemediation),
		WarningsWriter(&warningOutput),
		AllowedSystemServices([]SystemServiceControlGrant{
			{Service: "mysql.service", Actions: []SystemServiceAction{SystemServiceRead, "stop", SystemServiceReload}},
			{Service: "ignored.service", Actions: []SystemServiceAction{"enable"}},
		}),
	)
	require.NoError(t, err)
	defer runner.Close()

	require.NoError(t, runner.authorizeSystemServices(SystemServiceRead, "mysql.service"))
	require.NoError(t, runner.authorizeSystemServices(SystemServiceReload, "mysql.service"))
	assert.NotContains(t, runner.allowedSystemServices, "ignored.service")

	warnings := runner.Warnings()
	require.Len(t, warnings, 2)
	assert.Contains(t, warningOutput.String(), `AllowedSystemServices: skipping unsupported action "stop" in grant 0 for "mysql.service"`)
	assert.Contains(t, warningOutput.String(), `AllowedSystemServices: skipping unsupported action "enable" in grant 1 for "ignored.service"`)
}

func TestAuthorizeSystemServicesRejectsInvalidRequests(t *testing.T) {
	runner, err := New(WithMode(ModeRemediation), AllowedSystemServices([]SystemServiceControlGrant{
		{
			Service: "mysql.service",
			Actions: []SystemServiceAction{SystemServiceRead},
		},
	}))
	require.NoError(t, err)
	defer runner.Close()

	tests := []struct {
		name     string
		action   SystemServiceAction
		services []string
		needle   string
	}{
		{name: "unknown action", action: "stop", services: []string{"mysql.service"}, needle: "unsupported systemd action"},
		{name: "no services", action: SystemServiceRead, needle: "at least one system service"},
		{name: "runtime resource separator", action: SystemServiceRead, services: []string{"tenant:mysql.service"}, needle: "must not contain ':'"},
		{name: "runtime glob", action: SystemServiceRead, services: []string{"mysql*.service"}, needle: "glob pattern"},
		{name: "runtime backslash path", action: SystemServiceRead, services: []string{"..\\mysql.service"}, needle: "path separator"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := runner.authorizeSystemServices(test.action, test.services...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.needle)
		})
	}
}
