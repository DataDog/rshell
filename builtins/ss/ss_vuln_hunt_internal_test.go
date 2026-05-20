// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Vulnerability-hunt regression tests for campaign 2026-05-20-gpt-5.5-cyber-3.

package ss

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVulnHuntBuiltinDeclaredVsImplemented_FilterPrecedenceMatrix(t *testing.T) {
	allProtocols := options{showTCP: true, showUDP: true, showUnix: true}

	showAllAndListenOnly := allProtocols
	showAllAndListenOnly.showAll = true
	showAllAndListenOnly.listenOnly = true
	assert.True(t, filterEntry(showAllAndListenOnly, socketEntry{kind: sockTCP4, state: "ESTAB"}))
	assert.True(t, filterEntry(showAllAndListenOnly, socketEntry{kind: sockTCP4, state: "LISTEN"}))
	assert.True(t, filterEntry(showAllAndListenOnly, socketEntry{kind: sockUnix, state: "UNKNOWN"}))

	ipv4Only := allProtocols
	ipv4Only.showAll = true
	ipv4Only.ipv4Only = true
	assert.True(t, filterEntry(ipv4Only, socketEntry{kind: sockTCP4, state: "ESTAB"}))
	assert.False(t, filterEntry(ipv4Only, socketEntry{kind: sockTCP6, state: "ESTAB"}))
	assert.True(t, filterEntry(ipv4Only, socketEntry{kind: sockUnix, state: "LISTEN"}), "IP filters must not drop Unix sockets")

	ipv6Only := allProtocols
	ipv6Only.showAll = true
	ipv6Only.ipv6Only = true
	assert.False(t, filterEntry(ipv6Only, socketEntry{kind: sockUDP4, state: "UNCONN"}))
	assert.True(t, filterEntry(ipv6Only, socketEntry{kind: sockUDP6, state: "UNCONN"}))
	assert.True(t, filterEntry(ipv6Only, socketEntry{kind: sockUnix, state: "LISTEN"}), "IP filters must not drop Unix sockets")

	bothFamilies := allProtocols
	bothFamilies.showAll = true
	bothFamilies.ipv4Only = true
	bothFamilies.ipv6Only = true
	assert.True(t, filterEntry(bothFamilies, socketEntry{kind: sockTCP4, state: "ESTAB"}))
	assert.True(t, filterEntry(bothFamilies, socketEntry{kind: sockTCP6, state: "ESTAB"}))
	assert.True(t, filterEntry(bothFamilies, socketEntry{kind: sockUDP4, state: "UNCONN"}))
	assert.True(t, filterEntry(bothFamilies, socketEntry{kind: sockUDP6, state: "UNCONN"}))
}
