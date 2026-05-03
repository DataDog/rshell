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

func TestExpandCaseVariants(t *testing.T) {
	cases := []Case{
		{
			ID:        "ssh",
			Title:     "SSH",
			Prompt:    "prompt {{IP}} {{LOG_ROOT}}",
			Variables: map[string]string{"LOG_ROOT": "base", "IP": "198.51.100.23"},
			Criteria:  []Criterion{{Name: "ip", Source: "final", Contains: "{{IP}}", Points: 1}},
			Variants: []CaseVariant{
				{ID: "seed-1"},
				{ID: "seed-2", Title: "SSH seed 2", Variables: map[string]string{"IP": "203.0.113.66"}},
			},
		},
		{ID: "plain", Prompt: "plain", Criteria: []Criterion{{Name: "plain", Source: "final", Contains: "plain", Points: 1}}},
	}

	expanded, err := ExpandCaseVariants(cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(expanded) != 3 {
		t.Fatalf("expanded case count = %d, want 3", len(expanded))
	}
	if expanded[0].ID != "ssh-seed-1" || expanded[1].ID != "ssh-seed-2" || expanded[2].ID != "plain" {
		t.Fatalf("expanded ids = %q, %q, %q", expanded[0].ID, expanded[1].ID, expanded[2].ID)
	}
	if expanded[1].Title != "SSH seed 2" {
		t.Fatalf("variant title = %q", expanded[1].Title)
	}
	if expanded[1].Variables["LOG_ROOT"] != "base" || expanded[1].Variables["IP"] != "203.0.113.66" {
		t.Fatalf("merged variables = %#v", expanded[1].Variables)
	}
}

func TestGenerateRemoteHostDiagnosticsFixtures(t *testing.T) {
	root := t.TempDir()
	if err := GenerateRemoteHostDiagnosticsFixtures(root); err != nil {
		t.Fatalf("GenerateRemoteHostDiagnosticsFixtures() error = %v", err)
	}

	fixtureRoot := RemoteHostDiagnosticsGeneratedFixtureRoot(root)
	wantLineCounts := map[string]int{
		"logs/datadog/agent.log":                                               1200,
		"logs/datadog/agent.log.1":                                             700,
		"logs/auth.log":                                                        1500,
		"logs/auth.log.1":                                                      700,
		"logs/app/service.log":                                                 1100,
		"logs/app/service.log.1":                                               650,
		"logs/nginx/access.log":                                                1800,
		"logs/nginx/access.log.1":                                              900,
		"logs/nginx/error.log":                                                 800,
		"logs/nginx/error.log.1":                                               600,
		"logs/system.log":                                                      900,
		"logs/system.log.1":                                                    650,
		"logs/debug-noise.log":                                                 1500,
		"container/host/var/log/datadog/agent.log":                             850,
		"container/host/var/log/syslog":                                        750,
		"holdout/logs/app/checkout.log":                                        760,
		"holdout/logs/nginx/access.log":                                        1050,
		"holdout/logs/system.log":                                              760,
		"holdout/logs/app/worker.log":                                          620,
		"holdout/logs/auth.log":                                                980,
		"holdout/logs/deploy.log":                                              620,
		"holdout/logs/security/auth-success.log":                               900,
		"holdout/logs/datadog/api-agent.log":                                   900,
		"holdout/logs/app/cart.log":                                            780,
		"holdout/logs/app/cart.log.1":                                          620,
		"holdout/logs/nginx/cart-access.log":                                   900,
		"holdout/logs/system-cart.log":                                         760,
		"container/var/log/.gitkeep":                                           0,
		"variants/public/dd-config-seed-17/logs/datadog/core-agent.log":        1200,
		"variants/public/dd-config-seed-17/logs/datadog/agent.log.1":           702,
		"variants/public/dd-config-seed-17/logs/debug-noise.log":               1501,
		"variants/public/ssh-seed-29/logs/security/secure.log":                 1200,
		"variants/public/ssh-seed-29/logs/security/secure.log.1":               620,
		"variants/public/orders-db-seed-33/logs/app/orders-service.log":        1100,
		"variants/public/orders-db-seed-33/logs/app/orders-service.log.1":      651,
		"variants/public/orders-db-seed-33/logs/nginx/orders-access.log":       1800,
		"variants/public/orders-db-seed-33/logs/nginx/orders-error.log":        800,
		"variants/public/orders-db-seed-33/logs/system-orders.log":             900,
		"variants/public/kube-cert-expired-seed-41/container/var/log/.gitkeep": 0,
		"variants/public/kube-cert-expired-seed-41/container/host/var/log/datadog/checks.log": 720,
		"variants/public/kube-cert-expired-seed-41/container/host/var/log/syslog":             680,
		"variants/public/dd-api-key-seed-53/logs/datadog/agent-api.log":                       900,
		"variants/public/dd-api-key-seed-53/logs/datadog/agent.log.1":                         701,
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

	holdoutAuthSuccess := string(readGeneratedFixture(t, fixtureRoot, "holdout/logs/security/auth-success.log"))
	if got := countLinesContaining(holdoutAuthSuccess, "Failed password for invalid user", "from 203.0.113.66"); got != 42 {
		t.Fatalf("holdout accepted-login brute-force failure count = %d, want 42", got)
	}
	assertContains(t, holdoutAuthSuccess, "Accepted password for backup from 203.0.113.66")

	holdoutAPIKey := string(readGeneratedFixture(t, fixtureRoot, "holdout/logs/datadog/api-agent.log"))
	assertContains(t, holdoutAPIKey, "api key validation failed")
	assertContains(t, holdoutAPIKey, "api_key_invalid")
	assertContains(t, holdoutAPIKey, "no config validation errors observed")

	holdoutCart := string(readGeneratedFixture(t, fixtureRoot, "holdout/logs/app/cart.log"))
	assertContains(t, holdoutCart, "ERR max number of clients reached")
	assertContains(t, holdoutCart, "postgres health status=OK pool=cart_rw active=32")

	holdoutCartRotated := string(readGeneratedFixture(t, fixtureRoot, "holdout/logs/app/cart.log.1"))
	assertContains(t, holdoutCartRotated, "db pool exhausted")
	// Previous-day rotated decoy carries no "old_incident" hint -- the model
	// must use the 2026-04-30 date and the dependency (postgres vs redis) to
	// distinguish it from the current 2026-05-01 redis-driven incident.
	assertContains(t, holdoutCartRotated, "suspected_client=reporting-worker")

	holdoutCartSystem := string(readGeneratedFixture(t, fixtureRoot, "holdout/logs/system-cart.log"))
	assertContains(t, holdoutCartSystem, "maxclients")
	assertContains(t, holdoutCartSystem, "connection count active=32")

	ddConfigVariant := string(readGeneratedFixture(t, fixtureRoot, "variants/public/dd-config-seed-17/logs/datadog/core-agent.log"))
	assertContains(t, ddConfigVariant, "transaction_id=rc-9137")
	assertContains(t, ddConfigVariant, "line=87")
	ddConfigVariantRotated := string(readGeneratedFixture(t, fixtureRoot, "variants/public/dd-config-seed-17/logs/datadog/agent.log.1"))
	// Decoy: rc-8831 line=42 yaml failure on 2026-05-01 that recovered. No
	// "adversarial" / "old_rotation" labels -- the model must disambiguate
	// using the timestamp and recovered=true.
	assertContains(t, ddConfigVariantRotated, "transaction_id=rc-8831 recovered=true")
	assertContains(t, ddConfigVariantRotated, "line=42 column=17")

	sshVariant := string(readGeneratedFixture(t, fixtureRoot, "variants/public/ssh-seed-29/logs/security/secure.log"))
	if got := countLinesContaining(sshVariant, "Failed password for invalid user", "from 192.0.2.88"); got != 73 {
		t.Fatalf("ssh variant failure count = %d, want 73", got)
	}
	sshVariantRotated := string(readGeneratedFixture(t, fixtureRoot, "variants/public/ssh-seed-29/logs/security/secure.log.1"))
	assertContains(t, sshVariantRotated, "Accepted password for backup from 192.0.2.88")

	ordersVariant := string(readGeneratedFixture(t, fixtureRoot, "variants/public/orders-db-seed-33/logs/app/orders-service.log"))
	assertContains(t, ordersVariant, "service=orders")
	assertContains(t, ordersVariant, "db pool exhausted")
	assertContains(t, ordersVariant, "suspected_client=reporting-worker")
	ordersVariantRotated := string(readGeneratedFixture(t, fixtureRoot, "variants/public/orders-db-seed-33/logs/app/orders-service.log.1"))
	// Decoy: a previous-day DNS-resolution 502 that recovered. No
	// "dns-red-herring" label is present.
	assertContains(t, ordersVariantRotated, "lookup payments.service.consul: no such host")
	assertContains(t, ordersVariantRotated, "recovered=true")

	kubeExpiredAgent := string(readGeneratedFixture(t, fixtureRoot, "variants/public/kube-cert-expired-seed-41/container/host/var/log/datadog/checks.log"))
	assertContains(t, kubeExpiredAgent, "x509: certificate has expired")
	kubeExpiredSyslog := string(readGeneratedFixture(t, fixtureRoot, "variants/public/kube-cert-expired-seed-41/container/host/var/log/syslog"))
	assertContains(t, kubeExpiredSyslog, "NotAfter=2026-05-01T23:59:59Z")
	// No "clock-healthy-red-herring" label: chronyd reports a synchronized
	// clock; the cert material is the actual cause and the model must reason
	// about cert NotAfter vs clock skew without a hint label.
	assertContains(t, kubeExpiredSyslog, "System clock synchronized stratum=2")

	apiKeyVariant := string(readGeneratedFixture(t, fixtureRoot, "variants/public/dd-api-key-seed-53/logs/datadog/agent-api.log"))
	assertContains(t, apiKeyVariant, "key_id=ak-5317")
	assertContains(t, apiKeyVariant, "api_key_invalid")
	apiKeyVariantRotated := string(readGeneratedFixture(t, fixtureRoot, "variants/public/dd-api-key-seed-53/logs/datadog/agent.log.1"))
	// Decoy: an early-day rc-8831 line=42 yaml failure that recovered. No
	// "not-current-incident" label -- the model must reject the teammate's
	// config-reload theory using timestamps and the actual current cause
	// (api_key_invalid in agent-api.log).
	assertContains(t, apiKeyVariantRotated, "transaction_id=rc-8831 recovered=true")
	assertContains(t, apiKeyVariantRotated, "line=42 column=17")
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
