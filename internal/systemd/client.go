// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package systemd

// Client implements the trusted systemd backends for one resolved target.
// Target paths are copied at construction so runner configuration remains
// immutable while commands execute.
type Client struct {
	target       Target
	vacuumPolicy *JournalVacuumPolicy
}

// NewClient creates a client for one resolved systemd target.
func NewClient(target Target, vacuumPolicy *JournalVacuumPolicy) *Client {
	target.JournalDirs = append([]string(nil), target.JournalDirs...)
	client := &Client{target: target}
	if vacuumPolicy != nil {
		policyCopy := *vacuumPolicy
		client.vacuumPolicy = &policyCopy
	}
	return client
}
