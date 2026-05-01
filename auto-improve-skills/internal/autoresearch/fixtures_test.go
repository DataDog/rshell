// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package autoresearch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateRemoteHostDiagnosticsFixtures(t *testing.T) {
	root := t.TempDir()
	if err := GenerateRemoteHostDiagnosticsFixtures(root); err != nil {
		t.Fatalf("GenerateRemoteHostDiagnosticsFixtures() error = %v", err)
	}

	fixtureRoot := RemoteHostDiagnosticsGeneratedFixtureRoot(root)
	wantLineCounts := map[string]int{
		"logs/datadog/agent.log":                   1200,
		"logs/datadog/agent.log.1":                 700,
		"logs/auth.log":                            1500,
		"logs/auth.log.1":                          700,
		"logs/app/service.log":                     1100,
		"logs/app/service.log.1":                   650,
		"logs/nginx/access.log":                    1800,
		"logs/nginx/access.log.1":                  900,
		"logs/nginx/error.log":                     800,
		"logs/nginx/error.log.1":                   600,
		"logs/system.log":                          900,
		"logs/system.log.1":                        650,
		"logs/debug-noise.log":                     1500,
		"container/host/var/log/datadog/agent.log": 850,
		"container/host/var/log/syslog":            750,
		"holdout/logs/app/checkout.log":            760,
		"holdout/logs/nginx/access.log":            1050,
		"holdout/logs/system.log":                  760,
		"holdout/logs/app/worker.log":              620,
		"holdout/logs/auth.log":                    980,
		"holdout/logs/deploy.log":                  620,
		"container/var/log/.gitkeep":               0,
	}
	for rel, want := range wantLineCounts {
		data := readGeneratedFixture(t, fixtureRoot, rel)
		if got := strings.Count(string(data), "\n"); got != want {
			t.Fatalf("%s line count = %d, want %d", rel, got, want)
		}
		if want != 0 && (want < 500 || want > 2000) {
			t.Fatalf("%s line count %d is outside expected benchmark fixture range", rel, want)
		}
	}

	agent := string(readGeneratedFixture(t, fixtureRoot, "logs/datadog/agent.log"))
	assertContains(t, agent, "remote config applied transaction_id=rc-8831")
	assertContains(t, agent, "line=42")
	assertContains(t, agent, "no metrics flushed since 2026-04-30T10:12:03Z")

	auth := string(readGeneratedFixture(t, fixtureRoot, "logs/auth.log"))
	if got := countLinesContaining(auth, "Failed password for invalid user", "from 198.51.100.23"); got != 96 {
		t.Fatalf("suspicious brute-force failure count = %d, want 96", got)
	}
	assertContains(t, auth, "Accepted publickey for deploy from 203.0.113.8")

	service := string(readGeneratedFixture(t, fixtureRoot, "logs/app/service.log"))
	assertContains(t, service, "db pool exhausted")
	assertContains(t, service, "suspected_client=reporting-worker")

	system := string(readGeneratedFixture(t, fixtureRoot, "logs/system.log"))
	assertContains(t, system, "remaining connection slots are reserved")
	assertContains(t, system, "reporting-worker connection fanout")

	containerAgent := string(readGeneratedFixture(t, fixtureRoot, "container/host/var/log/datadog/agent.log"))
	assertContains(t, containerAgent, "kubernetes_apiserver")
	assertContains(t, containerAgent, "x509: certificate is not yet valid")

	containerSyslog := string(readGeneratedFixture(t, fixtureRoot, "container/host/var/log/syslog"))
	assertContains(t, containerSyslog, "chronyd")
	assertContains(t, containerSyslog, "clock")
	assertContains(t, containerSyslog, "skew")

	holdoutCheckout := string(readGeneratedFixture(t, fixtureRoot, "holdout/logs/app/checkout.log"))
	assertContains(t, holdoutCheckout, "lookup payments.service.consul")
	assertContains(t, holdoutCheckout, "postgres health status=OK")

	holdoutSystem := string(readGeneratedFixture(t, fixtureRoot, "holdout/logs/system.log"))
	assertContains(t, holdoutSystem, "SERVFAIL")
	assertContains(t, holdoutSystem, "payments.service.consul")

	holdoutWorker := string(readGeneratedFixture(t, fixtureRoot, "holdout/logs/app/worker.log"))
	assertContains(t, holdoutWorker, "received signal signal=SIGTERM")
	assertContains(t, holdoutWorker, "same build after restart")

	holdoutDeploy := string(readGeneratedFixture(t, fixtureRoot, "holdout/logs/deploy.log"))
	assertContains(t, holdoutDeploy, "dep-771")
	assertContains(t, holdoutDeploy, "completed status=success")
}

func readGeneratedFixture(t *testing.T, fixtureRoot, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read generated fixture %s: %v", rel, err)
	}
	return data
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("generated fixture missing %q", needle)
	}
}

func countLinesContaining(s string, needles ...string) int {
	count := 0
	for _, line := range strings.Split(s, "\n") {
		matches := true
		for _, needle := range needles {
			if !strings.Contains(line, needle) {
				matches = false
				break
			}
		}
		if matches {
			count++
		}
	}
	return count
}
