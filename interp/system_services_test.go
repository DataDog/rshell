// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
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

func TestAllowedSystemServicesRequiresRemediationMode(t *testing.T) {
	runner, err := New(AllowedSystemServices([]SystemServiceControlGrant{
		{
			Service: "mysql.service",
			Actions: []SystemServiceAction{SystemServiceRead},
		},
	}))
	require.NoError(t, err)
	defer runner.Close()

	err = runner.authorizeSystemServices(SystemServiceRead, "mysql.service")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "require remediation mode")
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

func TestAllowedSystemServicesRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		grant  SystemServiceControlGrant
		needle string
	}{
		{
			name:   "empty service",
			grant:  SystemServiceControlGrant{Service: "", Actions: []SystemServiceAction{SystemServiceRead}},
			needle: "must not be empty",
		},
		{
			name:   "whitespace",
			grant:  SystemServiceControlGrant{Service: "mysql service", Actions: []SystemServiceAction{SystemServiceRead}},
			needle: "whitespace or control characters",
		},
		{
			name:   "unicode whitespace",
			grant:  SystemServiceControlGrant{Service: "mysql\u00a0service", Actions: []SystemServiceAction{SystemServiceRead}},
			needle: "whitespace or control characters",
		},
		{
			name:   "path",
			grant:  SystemServiceControlGrant{Service: "/etc/systemd/system/mysql.service", Actions: []SystemServiceAction{SystemServiceRead}},
			needle: "path separator",
		},
		{
			name:   "glob",
			grant:  SystemServiceControlGrant{Service: "mysql*.service", Actions: []SystemServiceAction{SystemServiceRead}},
			needle: "glob pattern",
		},
		{
			name:   "no actions",
			grant:  SystemServiceControlGrant{Service: "mysql.service"},
			needle: "has no actions",
		},
		{
			name:   "unknown action",
			grant:  SystemServiceControlGrant{Service: "mysql.service", Actions: []SystemServiceAction{"stop"}},
			needle: `unsupported action "stop"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner, err := New(AllowedSystemServices([]SystemServiceControlGrant{test.grant}))
			if runner != nil {
				runner.Close()
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.needle)
		})
	}
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
		{name: "unknown action", action: "stop", services: []string{"mysql.service"}, needle: "unsupported system service action"},
		{name: "no services", action: SystemServiceRead, needle: "at least one system service"},
		{name: "runtime glob", action: SystemServiceRead, services: []string{"mysql*.service"}, needle: "glob pattern"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := runner.authorizeSystemServices(test.action, test.services...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.needle)
		})
	}
}
