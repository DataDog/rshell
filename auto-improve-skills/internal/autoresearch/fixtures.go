// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package autoresearch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const remoteHostDiagnosticsBenchmarkRel = "auto-improve-skills/benchmarks/remote-host-diagnostics"

// RemoteHostDiagnosticsBenchmarkDir returns the benchmark directory for the
// remote-host-diagnostics skill.
func RemoteHostDiagnosticsBenchmarkDir(root string) string {
	return filepath.Join(root, filepath.FromSlash(remoteHostDiagnosticsBenchmarkRel))
}

// RemoteHostDiagnosticsGeneratedFixtureRoot returns the gitignored directory
// where deterministic fixture logs are generated for benchmark runs.
func RemoteHostDiagnosticsGeneratedFixtureRoot(root string) string {
	return filepath.Join(RemoteHostDiagnosticsBenchmarkDir(root), "generated-fixtures")
}

// GenerateRemoteHostDiagnosticsFixtures creates deterministic, realistic log
// fixtures used by the remote-host-diagnostics benchmark. Generated logs are
// intentionally not committed; the benchmark runner recreates them before use.
func GenerateRemoteHostDiagnosticsFixtures(root string) error {
	fixtureRoot := RemoteHostDiagnosticsGeneratedFixtureRoot(root)
	if err := os.RemoveAll(fixtureRoot); err != nil {
		return fmt.Errorf("remove old generated fixtures: %w", err)
	}

	files := remoteHostDiagnosticsBaseFixtureFiles()
	files = append(files, remoteHostDiagnosticsPublicVariantFixtureFiles()...)

	for _, file := range files {
		if err := writeFixtureLines(filepath.Join(fixtureRoot, filepath.FromSlash(file.path)), file.lines); err != nil {
			return err
		}
	}
	for _, rel := range remoteHostDiagnosticsEmptyFixtureDirs() {
		if err := os.MkdirAll(filepath.Join(fixtureRoot, filepath.FromSlash(rel)), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(fixtureRoot, filepath.FromSlash(rel), ".gitkeep"), nil, 0o644); err != nil {
			return err
		}
	}
	return nil
}

type fixtureFile struct {
	path  string
	lines []string
}

func remoteHostDiagnosticsBaseFixtureFiles() []fixtureFile {
	return []fixtureFile{
		{path: "logs/datadog/agent.log", lines: generateDatadogAgentLog()},
		{path: "logs/datadog/agent.log.1", lines: generateDatadogAgentRotatedLog()},
		{path: "logs/auth.log", lines: generateAuthLog()},
		{path: "logs/auth.log.1", lines: generateAuthRotatedLog()},
		{path: "logs/app/service.log", lines: generateCheckoutServiceLog()},
		{path: "logs/app/service.log.1", lines: generateCheckoutServiceRotatedLog()},
		{path: "logs/nginx/access.log", lines: generateNginxAccessLog()},
		{path: "logs/nginx/access.log.1", lines: generateNginxAccessRotatedLog()},
		{path: "logs/nginx/error.log", lines: generateNginxErrorLog()},
		{path: "logs/nginx/error.log.1", lines: generateNginxErrorRotatedLog()},
		{path: "logs/system.log", lines: generateSystemLog()},
		{path: "logs/system.log.1", lines: generateSystemRotatedLog()},
		{path: "logs/debug-noise.log", lines: generateDebugNoiseLog()},
		{path: "container/host/var/log/datadog/agent.log", lines: generateContainerAgentLog()},
		{path: "container/host/var/log/syslog", lines: generateContainerSyslog()},
		{path: "holdout/logs/app/checkout.log", lines: generateHoldoutCheckoutLog()},
		{path: "holdout/logs/nginx/access.log", lines: generateHoldoutNginxAccessLog()},
		{path: "holdout/logs/system.log", lines: generateHoldoutSystemLog()},
		{path: "holdout/logs/app/worker.log", lines: generateHoldoutWorkerLog()},
		{path: "holdout/logs/auth.log", lines: generateHoldoutAuthLog()},
		{path: "holdout/logs/deploy.log", lines: generateHoldoutDeployLog()},
		{path: "holdout/logs/security/auth-success.log", lines: generateHoldoutAuthSuccessLog()},
		{path: "holdout/logs/datadog/api-agent.log", lines: generateHoldoutDatadogAPIKeyLog()},
		{path: "holdout/logs/app/cart.log", lines: generateHoldoutCartLog()},
		{path: "holdout/logs/app/cart.log.1", lines: generateHoldoutCartRotatedLog()},
		{path: "holdout/logs/nginx/cart-access.log", lines: generateHoldoutCartNginxAccessLog()},
		{path: "holdout/logs/system-cart.log", lines: generateHoldoutCartSystemLog()},
	}
}

func remoteHostDiagnosticsPublicVariantFixtureFiles() []fixtureFile {
	files := []fixtureFile{}

	ddConfigRoot := "variants/public/dd-config-seed-17/logs"
	files = append(files,
		fixtureFile{path: ddConfigRoot + "/datadog/core-agent.log", lines: replaceFixtureLines(generateDatadogAgentLog(),
			"2026-04-30T10", "2026-05-02T12",
			"2026-04-30T", "2026-05-02T",
			"host=checkout-01", "host=payments-17",
			"rc-8831", "rc-9137",
			"rc-8832", "rc-9138",
			"line=42", "line=87",
			"line 42", "line 87",
			"column=17", "column=9",
		)},
		// Decoy: a same-location (datadog.yaml line=42) YAML parse failure on
		// 2026-05-01 under the canonical rc-8831 transaction id, recovered after
		// retry. The current incident in this seed lives in core-agent.log under
		// rc-9137 line=87, so any answer that points at line=42 / rc-8831 is
		// reading the rotated decoy instead of the actual cause.
		fixtureFile{path: ddConfigRoot + "/datadog/agent.log.1", lines: appendFixtureLines(generateDatadogAgentRotatedLog(),
			"2026-05-01T23:51:14Z ERROR config validation failed file=/etc/datadog-agent/datadog.yaml line=42 column=17 error=\"yaml: mapping values are not allowed in this context\" transaction_id=rc-8831 recovered=true",
			"2026-05-01T23:51:22Z INFO config validation recovered transaction_id=rc-8831 attempt=2 status=OK",
		)},
		fixtureFile{path: ddConfigRoot + "/debug-noise.log", lines: appendFixtureLines(generateDebugNoiseLog(),
			"2026-05-02T11:58:00Z ERROR component=fixture-noise message=\"transient api_key_invalid canary cleared\" token=variant-noise",
		)},
	)

	sshRoot := "variants/public/ssh-seed-29/logs"
	files = append(files,
		fixtureFile{path: sshRoot + "/security/secure.log", lines: generateSSHAuthVariantLog(sshAuthVariantConfig{SourceIP: "192.0.2.88", FailureCount: 73, OtherAcceptedIP: "203.0.113.19", Start: time.Date(2026, 5, 2, 8, 30, 0, 0, time.UTC)})},
		fixtureFile{path: sshRoot + "/security/secure.log.1", lines: generateSSHAuthVariantRotatedLog("192.0.2.88", time.Date(2026, 5, 1, 21, 0, 0, 0, time.UTC))},
	)

	ordersRoot := "variants/public/orders-db-seed-33/logs"
	orderReplacements := []string{
		"checkout", "orders",
		"Checkout", "Orders",
		"req-", "ord-",
	}
	files = append(files,
		fixtureFile{path: ordersRoot + "/app/orders-service.log", lines: replaceFixtureLines(generateCheckoutServiceLog(), orderReplacements...)},
		// Decoy: a previous-day DNS-resolution 502 on the orders payments
		// upstream that recovered. The current incident is DB pool exhaustion;
		// any answer that blames DNS is following the rotated red herring.
		fixtureFile{path: ordersRoot + "/app/orders-service.log.1", lines: appendFixtureLines(replaceFixtureLines(generateCheckoutServiceRotatedLog(), orderReplacements...),
			"2026-05-01T22:44:10Z ERROR service=orders request failed id=ord-old-900 route=/api/orders status=502 error=\"lookup payments.service.consul: no such host\" recovered=true",
		)},
		fixtureFile{path: ordersRoot + "/nginx/orders-access.log", lines: replaceFixtureLines(generateNginxAccessLog(), orderReplacements...)},
		fixtureFile{path: ordersRoot + "/nginx/orders-error.log", lines: replaceFixtureLines(generateNginxErrorLog(), orderReplacements...)},
		fixtureFile{path: ordersRoot + "/system-orders.log", lines: replaceFixtureLines(generateSystemLog(), orderReplacements...)},
	)

	kubeExpiredRoot := "variants/public/kube-cert-expired-seed-41/container/host/var/log"
	files = append(files,
		fixtureFile{path: kubeExpiredRoot + "/datadog/checks.log", lines: generateContainerExpiredAgentLog()},
		fixtureFile{path: kubeExpiredRoot + "/syslog", lines: generateContainerExpiredSyslog()},
	)

	ddAPIKeyRoot := "variants/public/dd-api-key-seed-53/logs"
	files = append(files,
		fixtureFile{path: ddAPIKeyRoot + "/datadog/agent-api.log", lines: replaceFixtureLines(generateHoldoutDatadogAPIKeyLog(),
			"holdout-api", "public-api-seed-53",
			"ak-2209", "ak-5317",
			"2026-05-01T11", "2026-05-02T13",
			"rc-9901", "rc-5311",
			"rc-9902", "rc-5312",
		)},
		// Decoy: an early-day rc-8831 line=42 YAML failure that recovered.
		// The current incident is API key rejection (api_key_invalid /
		// status=403) for ak-5317, not a config validation failure; an answer
		// that points at line=42 / rc-8831 is reading the rotated decoy.
		fixtureFile{path: ddAPIKeyRoot + "/datadog/agent.log.1", lines: appendFixtureLines(generateDatadogAgentRotatedLog(),
			"2026-05-01T03:18:22Z ERROR config validation failed file=/etc/datadog-agent/datadog.yaml line=42 column=17 transaction_id=rc-8831 recovered=true",
		)},
	)

	return files
}

func remoteHostDiagnosticsEmptyFixtureDirs() []string {
	return []string{
		"container/var/log",
		"variants/public/kube-cert-expired-seed-41/container/var/log",
	}
}

func replaceFixtureLines(lines []string, replacements ...string) []string {
	replacer := strings.NewReplacer(replacements...)
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = replacer.Replace(line)
	}
	return out
}

func appendFixtureLines(lines []string, extra ...string) []string {
	out := append([]string{}, lines...)
	return append(out, extra...)
}

type sshAuthVariantConfig struct {
	SourceIP        string
	FailureCount    int
	OtherAcceptedIP string
	Start           time.Time
}

func generateSSHAuthVariantLog(cfg sshAuthVariantConfig) []string {
	users := []string{"admin", "root", "oracle", "postgres", "test", "ubuntu", "deploy", "backup", "guest", "ci", "jenkins", "support"}
	failures := map[int]int{}
	for n := 0; n < cfg.FailureCount; n++ {
		failures[360+n*6] = n
	}
	events := map[int]string{
		82:  fmt.Sprintf("login-gw sshd[3210]: Accepted publickey for deploy from %s port 61200 ssh2: RSA SHA256:variant-deploy", cfg.OtherAcceptedIP),
		190: "login-gw sudo:   deploy : TTY=pts/3 ; PWD=/srv/app ; USER=root ; COMMAND=/usr/bin/journalctl -n 20",
		350: fmt.Sprintf("login-gw sshd[3301]: Invalid user admin from %s port 55100", cfg.SourceIP),
		612: fmt.Sprintf("login-gw sshd[3701]: maximum authentication attempts exceeded for invalid user support from %s port 55320 ssh2 [preauth]", cfg.SourceIP),
		912: fmt.Sprintf("login-gw sshd[3900]: Accepted publickey for release from %s port 61244 ssh2: ED25519 SHA256:variant-release", cfg.OtherAcceptedIP),
	}
	lines := make([]string, 0, 1200)
	for i := 0; i < 1200; i++ {
		dt := cfg.Start.Add(time.Duration(i) * time.Second)
		if n, ok := failures[i]; ok {
			user := users[n%len(users)]
			lines = append(lines, fmt.Sprintf("%s login-gw sshd[%d]: Failed password for invalid user %s from %s port %d ssh2", syslogTime(dt), 3500+n, user, cfg.SourceIP, 55100+n))
		} else if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", syslogTime(dt), event))
		} else if i%149 == 0 {
			lines = append(lines, fmt.Sprintf("%s login-gw sshd[%d]: Failed password for invalid user scanner from 198.51.100.%d port %d ssh2", syslogTime(dt), 4200+i, 30+i%40, 47000+i))
		} else if i%101 == 0 {
			lines = append(lines, fmt.Sprintf("%s login-gw sudo:   deploy : TTY=pts/3 ; PWD=/srv/app ; USER=root ; COMMAND=/usr/bin/systemctl status ssh.service token=ssh-variant-sudo", syslogTime(dt)))
		} else {
			lines = append(lines, fmt.Sprintf("%s login-gw CRON[%d]: pam_unix(cron:session): session closed for user root token=ssh-variant-noise-%04d", syslogTime(dt), 5000+i, i))
		}
	}
	return lines
}

func generateSSHAuthVariantRotatedLog(sourceIP string, start time.Time) []string {
	lines := make([]string, 0, 620)
	for i := 0; i < 620; i++ {
		dt := start.Add(time.Duration(i*2) * time.Second)
		switch {
		case i == 222:
			// Decoy: an accepted password login for the same source IP from the
			// previous day's rotated log. The current-window suspicious source
			// did not get in; an answer that cites this rotated entry as a
			// successful current login is reading the previous-day decoy.
			lines = append(lines, fmt.Sprintf("%s login-gw sshd[5222]: Accepted password for backup from %s port 50022 ssh2", syslogTime(dt), sourceIP))
		case i%83 == 0:
			lines = append(lines, fmt.Sprintf("%s login-gw sshd[%d]: Failed password for invalid user temp from 203.0.113.%d port %d ssh2", syslogTime(dt), 6000+i, 40+i%20, 48000+i))
		default:
			lines = append(lines, fmt.Sprintf("%s login-gw CRON[%d]: pam_unix(cron:session): session closed for user root token=ssh-variant-rotated-%04d", syslogTime(dt), 7000+i, i))
		}
	}
	return lines
}

func generateContainerExpiredAgentLog() []string {
	start := time.Date(2026, 5, 2, 6, 0, 0, 0, time.UTC)
	events := map[int]string{
		0:   "INFO agent container boot version=7.99.0 container_id=fixture-expired host_mount=/host/var/log",
		94:  "INFO collector check completed check=kubelet status=OK latency_ms=29",
		211: "ERROR collector check failed check=kubernetes_apiserver error=\"x509: certificate has expired or is not yet valid: current time 2026-05-02T06:03:31Z is after 2026-05-01T23:59:59Z\" endpoint=https://10.96.0.1:443",
		212: "WARN collector skipped check=kubernetes_apiserver reason=\"tls handshake failure\" next_retry=15s",
		286: "ERROR collector check failed check=kubernetes_apiserver error=\"x509: certificate has expired\" tls_server_name=kubernetes.default.svc cert_not_after=2026-05-01T23:59:59Z",
		420: "INFO collector check completed check=container status=OK latency_ms=18",
		522: "ERROR collector check failed check=kubernetes_apiserver error=\"x509: certificate has expired\" endpoint=https://10.96.0.1:443",
	}
	checks := []string{"container", "docker", "kubelet", "process", "network", "kubernetes_state_core"}
	lines := make([]string, 0, 720)
	for i := 0; i < 720; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", isoTime(dt), event))
		} else if i%131 == 0 {
			lines = append(lines, fmt.Sprintf("%s WARN collector slow check=%s duration_ms=%d recovered=true token=expired-cert-noise-%04d", isoTime(dt), checks[i%len(checks)], 210+i%70, i))
		} else {
			lines = append(lines, fmt.Sprintf("%s DEBUG collector heartbeat check=%s status=OK sequence=%04d token=expired-cert-noise", isoTime(dt), checks[i%len(checks)], i))
		}
	}
	return lines
}

func generateContainerExpiredSyslog() []string {
	start := time.Date(2026, 5, 2, 6, 0, 0, 0, time.UTC)
	events := map[int]string{
		// Decoy: clock is reported synchronized, but the apiserver serving
		// certificate has expired (NotAfter has passed) -- the cert material
		// problem is the actual cause, not clock skew.
		18:  "node chronyd[801]: System clock synchronized stratum=2 offset=0.001s",
		136: "node kubelet[22]: apiserver serving certificate NotAfter=2026-05-01T23:59:59Z has passed; rotation controller pending",
		208: "node datadog-agent[17]: kubernetes_apiserver check failing: x509 certificate has expired (node clock synchronized)",
		244: "node cert-rotation[77]: renewal request queued for kubernetes.default.svc serving certificate status=pending approval",
		390: "node chronyd[801]: Selected source 192.0.2.10 (time.example) offset=0.0009s",
	}
	lines := make([]string, 0, 680)
	for i := 0; i < 680; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", syslogTime(dt), event))
		} else if i%121 == 0 {
			lines = append(lines, fmt.Sprintf("%s node kubelet[22]: pod sandbox changed pod=fixture-expired-noise-%d namespace=default", syslogTime(dt), i))
		} else {
			lines = append(lines, fmt.Sprintf("%s node systemd[1]: fixture heartbeat unit=container-runtime.service sequence=%04d token=expired-cert-syslog", syslogTime(dt), i))
		}
	}
	return lines
}

func writeFixtureLines(path string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func isoTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

func syslogTime(t time.Time) string {
	return t.UTC().Format("Jan 02 15:04:05")
}

func nginxTime(t time.Time) string {
	return t.UTC().Format("02/Jan/2006:15:04:05 +0000")
}

func nginxErrorTime(t time.Time) string {
	return t.UTC().Format("2006/01/02 15:04:05")
}

func generateDatadogAgentLog() []string {
	start := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	checks := []string{"cpu", "disk", "network", "ntp", "postgres", "redisdb", "http_check", "process", "container"}
	events := map[int]string{
		2:    "INFO agent starting version=7.99.0 build=fixture commit=8e3d1 env=prod host=checkout-01",
		9:    "INFO config loaded from /etc/datadog-agent/datadog.yaml sources=file,environment remote_config=true",
		52:   "INFO collector check completed check=postgres status=OK latency_ms=18",
		129:  "WARN flare skipped component=diagnose reason=\"not requested\"",
		216:  "WARN forwarder retryable error domain=intake endpoint=/api/v1/series status=429 retry_in=10s recovered=true",
		228:  "INFO forwarder recovered domain=intake endpoint=/api/v1/series status=202",
		360:  "INFO remote config poll complete transaction_id=rc-8818 changed=false products=agent_config,apm_sampling",
		454:  "WARN collector check failed check=redisdb error=\"i/o timeout\" retrying=true",
		466:  "INFO collector check recovered check=redisdb status=OK latency_ms=24",
		643:  "INFO remote config applied transaction_id=rc-8830 product=apm_sampling version=314159 changed=true",
		650:  "INFO trace-agent config reloaded transaction_id=rc-8830 status=OK",
		702:  "INFO remote config applied transaction_id=rc-8831 product=agent_config version=271828 changed=true source=remote-config",
		714:  "INFO config reload requested source=remote-config transaction_id=rc-8831 path=/etc/datadog-agent/datadog.yaml",
		722:  "ERROR config validation failed file=/etc/datadog-agent/datadog.yaml line=42 column=17 key=logs_config error=\"yaml: mapping values are not allowed in this context\" transaction_id=rc-8831",
		723:  "ERROR core agent stopped: invalid configuration after remote-config reload transaction_id=rc-8831",
		724:  "WARN aggregator stopped; skipping metric flush last_success=2026-04-30T10:11:58Z",
		725:  "WARN forwarder paused because aggregator is stopped pending_series=1842",
		731:  "INFO trace-agent still running status=OK note=\"APM intake is healthy; core metrics agent is stopped\"",
		775:  "INFO retrying config load attempt=1 source=remote-config transaction_id=rc-8831",
		776:  "ERROR config validation failed file=/etc/datadog-agent/datadog.yaml line=42 column=17 key=logs_config error=\"yaml: mapping values are not allowed in this context\" transaction_id=rc-8831",
		846:  "WARN no metrics flushed since 2026-04-30T10:12:03Z reason=\"core agent stopped\"",
		918:  "INFO remote config poll complete transaction_id=rc-8832 changed=false products=agent_config,apm_sampling",
		969:  "ERROR collector scheduler disabled because core agent is not running",
		1031: "WARN no metrics flushed since 2026-04-30T10:12:03Z reason=\"invalid configuration\"",
		1120: "INFO trace-agent heartbeat status=OK spans_sent=293",
	}

	lines := make([]string, 0, 1200)
	for i := 0; i < 1200; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", isoTime(dt), event))
			continue
		}
		check := checks[(i*7)%len(checks)]
		switch {
		case i%137 == 0:
			lines = append(lines, fmt.Sprintf("%s WARN collector slow check=%s duration_ms=%d sample_id=agent-noise-%04d", isoTime(dt), check, 180+i%90, i))
		case i%113 == 0:
			lines = append(lines, fmt.Sprintf("%s ERROR log pipeline dropped message pipeline=app count=1 reason=\"invalid utf8\" sample_id=agent-noise-%04d", isoTime(dt), i))
		case i%29 == 0:
			lines = append(lines, fmt.Sprintf("%s DEBUG remote config poll skipped jitter_ms=%d transaction_id=rc-noop-%04d", isoTime(dt), 50+i%400, i))
		default:
			lines = append(lines, fmt.Sprintf("%s DEBUG collector check heartbeat check=%s status=OK sequence=%04d token=agent-noise", isoTime(dt), check, i))
		}
	}
	return lines
}

func generateDatadogAgentRotatedLog() []string {
	start := time.Date(2026, 4, 29, 23, 45, 0, 0, time.UTC)
	checks := []string{"cpu", "disk", "network", "ntp", "postgres", "redisdb", "http_check", "process", "container"}
	// Decoy: a same-location (datadog.yaml line=42) config validation failure on
	// the previous day, recovered after a few retries. A naive grep "line=42"
	// across agent.log* will surface this rotated entry alongside the current
	// incident; the model must use timestamps and the transaction id to
	// disambiguate. Indices reuse existing slots so total line count stays 700.
	events := map[int]string{
		14:  "INFO agent starting version=7.98.1 build=fixture host=checkout-01",
		119: "ERROR config validation failed file=/etc/datadog-agent/conf.d/http_check.d/conf.yaml line=17 error=\"missing required field url\" check=http_check recovered=true",
		131: "INFO collector check recovered check=http_check status=OK after_fix=true",
		311: "WARN forwarder retryable error domain=logs endpoint=/api/v2/logs status=503 retry_in=15s recovered=true",
		325: "INFO forwarder recovered domain=logs endpoint=/api/v2/logs status=202",
		333: "ERROR config validation failed file=/etc/datadog-agent/datadog.yaml line=42 column=17 key=logs_config error=\"yaml: mapping values are not allowed in this context\" transaction_id=rc-8651",
		334: "WARN aggregator paused; pending metric flush deferred transaction_id=rc-8651",
		340: "INFO config validation recovered transaction_id=rc-8651 attempt=3 status=OK",
		342: "INFO core agent resumed after config rollback transaction_id=rc-8651",
		512: "INFO remote config poll complete transaction_id=rc-8799 changed=false",
	}

	lines := make([]string, 0, 700)
	for i := 0; i < 700; i++ {
		dt := start.Add(time.Duration(i*2) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", isoTime(dt), event))
		} else if i%41 == 0 {
			lines = append(lines, fmt.Sprintf("%s WARN collector transient check=%s error=\"temporary network timeout\" recovered=true token=old-noise-%04d", isoTime(dt), checks[i%len(checks)], i))
		} else {
			lines = append(lines, fmt.Sprintf("%s DEBUG agent previous-rotation heartbeat sequence=%04d token=old-agent-noise", isoTime(dt), i))
		}
	}
	return lines
}

func generateAuthLog() []string {
	start := time.Date(2026, 4, 30, 9, 45, 0, 0, time.UTC)
	users := []string{"admin", "root", "oracle", "postgres", "test", "ubuntu", "deploy", "backup", "guest", "ci", "jenkins", "support", "mysql", "elastic", "git", "prometheus"}
	failures := map[int]int{}
	for n := 0; n < 96; n++ {
		failures[785+n*4] = n
	}
	events := map[int]string{
		61:   "bastion sshd[1410]: Accepted publickey for deploy from 203.0.113.8 port 61200 ssh2: RSA SHA256:fixture-deploy",
		130:  "bastion sudo:   deploy : TTY=pts/0 ; PWD=/srv/app ; USER=root ; COMMAND=/usr/bin/systemctl status checkout.service",
		405:  "bastion sshd[1501]: Failed password for invalid user admin from 192.0.2.50 port 51220 ssh2",
		501:  "bastion sshd[1502]: Failed password for invalid user root from 192.0.2.50 port 51221 ssh2",
		693:  "bastion sshd[1510]: Accepted publickey for release from 198.51.100.77 port 49212 ssh2: ED25519 SHA256:fixture-release",
		754:  "bastion sshd[1512]: Invalid user postgres from 198.51.100.23 port 52001",
		1172: "bastion sshd[1802]: maximum authentication attempts exceeded for invalid user support from 198.51.100.23 port 52320 ssh2 [preauth]",
		1244: "bastion sshd[1810]: Accepted publickey for deploy from 203.0.113.8 port 61244 ssh2: RSA SHA256:fixture-deploy",
		1328: "bastion sshd[1820]: Failed password for invalid user admin from 198.51.100.24 port 53220 ssh2",
		1398: "bastion sshd[1830]: Connection closed by authenticating user root 198.51.100.23 port 52444 [preauth]",
	}

	lines := make([]string, 0, 1500)
	for i := 0; i < 1500; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if n, ok := failures[i]; ok {
			user := users[n%len(users)]
			lines = append(lines, fmt.Sprintf("%s bastion sshd[%d]: Failed password for invalid user %s from 198.51.100.23 port %d ssh2", syslogTime(dt), 1600+n, user, 52000+n))
		} else if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", syslogTime(dt), event))
		} else if i%97 == 0 {
			lines = append(lines, fmt.Sprintf("%s bastion sshd[%d]: Failed password for invalid user scanner from 203.0.113.44 port %d ssh2", syslogTime(dt), 2000+i, 40000+i))
		} else if i%83 == 0 {
			lines = append(lines, fmt.Sprintf("%s bastion sshd[%d]: pam_unix(sshd:session): session opened for user deploy(uid=1001) by (uid=0)", syslogTime(dt), 2100+i))
		} else if i%67 == 0 {
			lines = append(lines, fmt.Sprintf("%s bastion sudo:   deploy : TTY=pts/0 ; PWD=/srv/app ; USER=root ; COMMAND=/usr/bin/journalctl -n 20", syslogTime(dt)))
		} else if i%31 == 0 {
			lines = append(lines, fmt.Sprintf("%s bastion sshd[%d]: Received disconnect from 203.0.113.%d port %d:11: disconnected by user", syslogTime(dt), 2200+i, 10+i%30, 41000+i))
		} else {
			lines = append(lines, fmt.Sprintf("%s bastion CRON[%d]: pam_unix(cron:session): session closed for user root token=auth-noise-%04d", syslogTime(dt), 3000+i, i))
		}
	}
	return lines
}

func generateAuthRotatedLog() []string {
	start := time.Date(2026, 4, 29, 22, 0, 0, 0, time.UTC)
	lines := make([]string, 0, 700)
	for i := 0; i < 700; i++ {
		dt := start.Add(time.Duration(i*3) * time.Second)
		if i%89 == 0 {
			lines = append(lines, fmt.Sprintf("%s bastion sshd[%d]: Failed password for invalid user temp from 203.0.113.%d port %d ssh2", syslogTime(dt), 4000+i, 60+i%20, 45000+i))
		} else if i == 321 {
			lines = append(lines, fmt.Sprintf("%s bastion sshd[4455]: Accepted publickey for deploy from 203.0.113.8 port 61111 ssh2: RSA SHA256:fixture-deploy", syslogTime(dt)))
		} else {
			lines = append(lines, fmt.Sprintf("%s bastion CRON[%d]: pam_unix(cron:session): session closed for user root token=auth-rotated-noise-%04d", syslogTime(dt), 5000+i, i))
		}
	}
	return lines
}

func generateCheckoutServiceLog() []string {
	start := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	routes := []string{"/api/cart", "/api/checkout", "/api/profile", "/api/promotions", "/health"}
	events := map[int]string{
		1:   "INFO service=checkout boot complete version=2026.04.30 build=fc9e3b config_source=file",
		62:  "INFO service=checkout handled request id=req-090062 route=/api/checkout status=200 latency_ms=44",
		183: "WARN service=checkout upstream retry id=req-090183 upstream=payments attempt=1 error=\"deadline exceeded\" recovered=true",
		197: "INFO service=checkout upstream recovered id=req-090197 upstream=payments status=OK",
		552: "WARN service=checkout db pool wait high pool=checkout_rw active=108 idle=0 max=120 wait_ms=450 db_host=db.internal db_port=5432",
		578: "WARN service=checkout dependency latency high dependency=postgres p95_ms=920 pool=checkout_rw active=116 max=120",
		594: "ERROR service=checkout db pool exhausted pool=checkout_rw active=120 max=120 wait_ms=3000 error=\"context deadline exceeded\" suspected_client=reporting-worker",
		595: "ERROR service=checkout request failed id=req-1015 route=/api/checkout status=500 error=\"database connection refused\" db_host=db.internal db_port=5432 pool=checkout_rw",
		601: "ERROR service=checkout request failed id=req-1016 route=/api/checkout status=500 error=\"pq: remaining connection slots are reserved for non-replication superuser connections\" db_host=db.internal db_port=5432 pool=checkout_rw",
		607: "ERROR service=checkout request failed id=req-1017 route=/api/checkout status=500 error=\"database connection refused\" db_host=db.internal db_port=5432 pool=checkout_rw",
		614: "WARN service=checkout circuit breaker opened dependency=postgres route=/api/checkout failure_rate=0.86 window=60s",
		639: "ERROR service=checkout request failed id=req-1021 route=/api/checkout status=502 error=\"upstream checkout worker unavailable after db timeout\"",
		683: "INFO service=checkout healthcheck status=degraded dependency=postgres pool=checkout_rw active=120 max=120",
		777: "WARN service=checkout cache miss spike cache=redis route=/api/cart cache_hit_rate=0.71",
		910: "INFO service=checkout payment gateway status=OK latency_ms=18",
	}

	lines := make([]string, 0, 1100)
	for i := 0; i < 1100; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", isoTime(dt), event))
			continue
		}
		route := routes[(i*5+3)%len(routes)]
		latency := 30 + (i*17)%180
		if i%149 == 0 {
			lines = append(lines, fmt.Sprintf("%s WARN service=checkout slow request id=req-%06d route=%s status=200 latency_ms=%d token=svc-noise-%04d", isoTime(dt), 90000+i, route, latency+400, i))
		} else if i%211 == 0 {
			lines = append(lines, fmt.Sprintf("%s ERROR service=checkout feature-flag refresh failed flag=promo_banner error=\"timeout\" recovered=true token=svc-noise-%04d", isoTime(dt), i))
		} else {
			lines = append(lines, fmt.Sprintf("%s INFO service=checkout handled request id=req-%06d route=%s status=200 latency_ms=%d token=svc-noise", isoTime(dt), 90000+i, route, latency))
		}
	}
	return lines
}

func generateCheckoutServiceRotatedLog() []string {
	start := time.Date(2026, 4, 29, 23, 20, 0, 0, time.UTC)
	lines := make([]string, 0, 650)
	for i := 0; i < 650; i++ {
		dt := start.Add(time.Duration(i*2) * time.Second)
		switch {
		case i == 188:
			lines = append(lines, fmt.Sprintf("%s ERROR service=checkout request failed id=req-old-188 route=/api/checkout status=500 error=\"feature flag parse failed\" recovered=true", isoTime(dt)))
		case i == 190:
			lines = append(lines, fmt.Sprintf("%s INFO service=checkout recovered route=/api/checkout status=200", isoTime(dt)))
		case i%73 == 0:
			lines = append(lines, fmt.Sprintf("%s WARN service=checkout slow request id=req-old-%d route=/api/cart latency_ms=%d recovered=true", isoTime(dt), i, 500+i%50))
		default:
			lines = append(lines, fmt.Sprintf("%s INFO service=checkout previous-rotation heartbeat sequence=%04d token=svc-rotated-noise", isoTime(dt), i))
		}
	}
	return lines
}

func generateNginxAccessLog() []string {
	start := time.Date(2026, 4, 30, 9, 50, 0, 0, time.UTC)
	checkoutFailures := map[int]int{
		1202: 500, 1205: 500, 1208: 500, 1211: 502, 1214: 500, 1217: 502, 1220: 500, 1224: 500,
		1230: 502, 1235: 500, 1240: 500, 1246: 502, 1252: 500, 1258: 500, 1264: 502, 1270: 500,
	}
	routes := []string{"/health", "/api/cart", "/api/checkout", "/api/profile", "/api/promotions"}
	lines := make([]string, 0, 1800)
	for i := 0; i < 1800; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		client := fmt.Sprintf("203.0.113.%d", 10+i%80)
		if code, ok := checkoutFailures[i]; ok {
			size := 148
			if code == 502 {
				size = 167
			}
			lines = append(lines, fmt.Sprintf("%s - - [%s] \"POST /api/checkout HTTP/1.1\" %d %d \"-\" \"fixture-client/%d\" request_id=req-%04d", client, nginxTime(dt), code, size, i%7, 1000+i))
		} else if i%227 == 0 {
			lines = append(lines, fmt.Sprintf("%s - - [%s] \"GET /api/search?q=fixture HTTP/1.1\" 500 211 \"-\" \"fixture-client/%d\" request_id=search-red-herring-%04d", client, nginxTime(dt), i%7, i))
		} else if i%131 == 0 {
			lines = append(lines, fmt.Sprintf("%s - - [%s] \"POST /api/login HTTP/1.1\" 429 98 \"-\" \"fixture-client/%d\" request_id=rate-noise-%04d", client, nginxTime(dt), i%7, i))
		} else {
			route := routes[i%len(routes)]
			method := "GET"
			if route == "/api/checkout" {
				method = "POST"
			}
			size := 400 + i%600
			userAgent := fmt.Sprintf("fixture-client/%d", i%7)
			if route == "/health" {
				size = 12
				userAgent = "kube-probe"
			}
			lines = append(lines, fmt.Sprintf("%s - - [%s] \"%s %s HTTP/1.1\" 200 %d \"-\" \"%s\" request_id=req-%04d", client, nginxTime(dt), method, route, size, userAgent, 1000+i))
		}
	}
	return lines
}

func generateNginxAccessRotatedLog() []string {
	start := time.Date(2026, 4, 29, 22, 30, 0, 0, time.UTC)
	routes := []string{"/health", "/api/cart", "/api/checkout", "/static/app.js"}
	lines := make([]string, 0, 900)
	for i := 0; i < 900; i++ {
		dt := start.Add(time.Duration(i*2) * time.Second)
		client := fmt.Sprintf("198.51.100.%d", 30+i%30)
		if i%173 == 0 {
			lines = append(lines, fmt.Sprintf("%s - - [%s] \"GET /api/search?q=old HTTP/1.1\" 500 201 \"-\" \"fixture-client-old\" request_id=old-search-%04d", client, nginxTime(dt), i))
		} else {
			route := routes[i%len(routes)]
			method := "GET"
			if route == "/api/checkout" {
				method = "POST"
			}
			lines = append(lines, fmt.Sprintf("%s - - [%s] \"%s %s HTTP/1.1\" 200 %d \"-\" \"fixture-client-old\" request_id=old-%04d", client, nginxTime(dt), method, route, 100+i%500, i))
		}
	}
	return lines
}

func generateNginxErrorLog() []string {
	start := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	events := map[int]string{
		602: "[error] 100#100: *420 upstream prematurely closed connection while reading response header from upstream, client: 203.0.113.13, server: checkout.example, request: \"POST /api/checkout HTTP/1.1\", upstream: \"http://127.0.0.1:8080/api/checkout\", request_id=req-2202",
		611: "[error] 100#100: *421 connect() failed (111: Connection refused) while connecting to upstream, client: 203.0.113.16, server: checkout.example, request: \"POST /api/checkout HTTP/1.1\", upstream: \"http://127.0.0.1:8080/api/checkout\", request_id=req-2211",
		627: "[error] 100#100: *422 upstream timed out (110: Operation timed out) while reading response header from upstream, client: 203.0.113.18, server: checkout.example, request: \"POST /api/checkout HTTP/1.1\", upstream: \"http://127.0.0.1:8080/api/checkout\", request_id=req-2227",
		660: "[warn] 100#100: *425 upstream server temporarily disabled while connecting to upstream, server: checkout.example, request: \"POST /api/checkout HTTP/1.1\", upstream: \"http://127.0.0.1:8080/api/checkout\"",
	}

	lines := make([]string, 0, 800)
	for i := 0; i < 800; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", nginxErrorTime(dt), event))
		} else if i%181 == 0 {
			lines = append(lines, fmt.Sprintf("%s [error] 100#100: *%d open() \"/usr/share/nginx/html/favicon.ico\" failed (2: No such file or directory), client: 203.0.113.%d, server: checkout.example, request: \"GET /favicon.ico HTTP/1.1\"", nginxErrorTime(dt), 300+i, i%80))
		} else if i%97 == 0 {
			lines = append(lines, fmt.Sprintf("%s [warn] 100#100: *%d an upstream response is buffered to a temporary file while reading upstream, client: 203.0.113.%d, request: \"GET /api/cart HTTP/1.1\"", nginxErrorTime(dt), 300+i, i%80))
		} else {
			lines = append(lines, fmt.Sprintf("%s [info] 100#100: *%d client keepalive closed connection token=nginx-error-noise-%04d", nginxErrorTime(dt), 300+i, i))
		}
	}
	return lines
}

func generateNginxErrorRotatedLog() []string {
	start := time.Date(2026, 4, 29, 22, 30, 0, 0, time.UTC)
	lines := make([]string, 0, 600)
	for i := 0; i < 600; i++ {
		dt := start.Add(time.Duration(i*2) * time.Second)
		if i == 277 {
			lines = append(lines, fmt.Sprintf("%s [error] 100#100: *88 upstream timed out while reading response header from upstream, request: \"GET /api/search HTTP/1.1\", recovered=true", nginxErrorTime(dt)))
		} else {
			lines = append(lines, fmt.Sprintf("%s [info] 100#100: *%d previous rotation keepalive closed token=nginx-rotated-noise-%04d", nginxErrorTime(dt), 80+i, i))
		}
	}
	return lines
}

func generateSystemLog() []string {
	start := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	events := map[int]string{
		0:   "host kernel: boot fixture host kernel=6.8.0-fixture",
		192: "host systemd[1]: Started checkout.service.",
		510: "host postgres[2190]: LOG: checkpoint complete: wrote 142 buffers (0.9%); 0 WAL files added",
		574: "host postgres[2200]: LOG: connection received: host=10.0.44.19 port=45100 application_name=reporting-worker user=reports",
		575: "host postgres[2200]: LOG: connection received: host=10.0.44.19 port=45101 application_name=reporting-worker user=reports",
		576: "host postgres[2200]: LOG: connection received: host=10.0.44.19 port=45102 application_name=reporting-worker user=reports",
		594: "host kernel: TCP: request_sock_TCP: Possible SYN flooding on port 5432. Sending cookies. Check SNMP counters.",
		600: "host postgres[2201]: FATAL: remaining connection slots are reserved for non-replication superuser connections",
		601: "host postgres[2202]: FATAL: sorry, too many clients already application_name=checkout-service user=checkout_rw database=shop",
		603: "host postgres[2203]: LOG: could not accept SSL connection: Connection reset by peer",
		607: "host postgres[2204]: LOG: connection rejected application_name=checkout-service reason=\"remaining connection slots reserved\" active=120 max_connections=120",
		640: "host systemd[1]: checkout.service: Watchdog timeout ignored in fixture",
		690: "host postgres[2210]: LOG: connection received: host=10.0.44.19 port=45190 application_name=reporting-worker user=reports",
		810: "host cron[3333]: reporting-worker connection fanout job still running elapsed=15m db=db.internal",
	}

	lines := make([]string, 0, 900)
	for i := 0; i < 900; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", syslogTime(dt), event))
		} else if i%157 == 0 {
			lines = append(lines, fmt.Sprintf("%s host kernel: audit: type=1400 apparmor=\"DENIED\" operation=\"open\" profile=\"fixture\" name=\"/tmp/noise-%d\" pid=%d comm=\"noise\"", syslogTime(dt), i, 6000+i))
		} else if i%103 == 0 {
			lines = append(lines, fmt.Sprintf("%s host systemd[1]: logrotate.service: Deactivated successfully token=system-noise-%04d", syslogTime(dt), i))
		} else {
			lines = append(lines, fmt.Sprintf("%s host systemd[1]: fixture heartbeat service=checkout.slice sequence=%04d token=system-noise", syslogTime(dt), i))
		}
	}
	return lines
}

func generateSystemRotatedLog() []string {
	start := time.Date(2026, 4, 29, 23, 0, 0, 0, time.UTC)
	lines := make([]string, 0, 650)
	for i := 0; i < 650; i++ {
		dt := start.Add(time.Duration(i*2) * time.Second)
		if i == 241 {
			lines = append(lines, fmt.Sprintf("%s host postgres[1200]: FATAL: password authentication failed for user \"readonly\" recovered=true", syslogTime(dt)))
		} else {
			lines = append(lines, fmt.Sprintf("%s host systemd[1]: previous rotation heartbeat sequence=%04d token=system-rotated-noise", syslogTime(dt), i))
		}
	}
	return lines
}

func generateDebugNoiseLog() []string {
	start := time.Date(2026, 4, 30, 8, 0, 0, 0, time.UTC)
	lines := make([]string, 0, 1500)
	for i := 0; i < 1500; i++ {
		dt := start.Add(time.Duration(i*2) * time.Second)
		level := "DEBUG"
		message := "background sampler tick"
		if i%211 == 0 {
			level = "ERROR"
			message = "synthetic canary failed but unrelated service=search"
		} else if i%97 == 0 {
			level = "WARN"
			message = "slow DNS lookup for analytics endpoint recovered=true"
		}
		lines = append(lines, fmt.Sprintf("%s %s component=fixture-noise sequence=%04d message=\"%s\" token=not-relevant", isoTime(dt), level, i, message))
	}
	return lines
}

func generateContainerAgentLog() []string {
	start := time.Date(2026, 4, 30, 3, 0, 0, 0, time.UTC)
	checks := []string{"container", "docker", "kubelet", "process", "network", "kubernetes_state_core"}
	events := map[int]string{
		0:   "INFO agent container boot version=7.99.0 container_id=fixture host_mount=/host/var/log",
		42:  "INFO collector check completed check=kubelet status=OK latency_ms=31",
		127: "WARN collector check failed check=kubernetes_state_core error=\"context deadline exceeded\" recovered=true",
		134: "INFO collector check recovered check=kubernetes_state_core status=OK",
		314: "ERROR collector check failed check=kubernetes_apiserver error=\"x509: certificate is not yet valid: current time 2026-04-30T03:05:14Z is before 2026-04-30T10:58:00Z\" endpoint=https://10.96.0.1:443",
		315: "WARN collector skipped check=kubernetes_apiserver reason=\"tls handshake failure\" next_retry=15s",
		374: "ERROR collector check failed check=kubernetes_apiserver error=\"x509: certificate is not yet valid: current time 2026-04-30T03:06:14Z is before 2026-04-30T10:58:00Z\" endpoint=https://10.96.0.1:443",
		438: "ERROR collector check failed check=kubernetes_apiserver error=\"x509: certificate is not yet valid\" tls_server_name=kubernetes.default.svc",
		512: "INFO collector check completed check=container status=OK latency_ms=22",
		640: "WARN flare skipped reason=\"benchmark read-only fixture\"",
		714: "ERROR collector check failed check=kubernetes_apiserver error=\"x509: certificate is not yet valid\" endpoint=https://10.96.0.1:443",
	}

	lines := make([]string, 0, 850)
	for i := 0; i < 850; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", isoTime(dt), event))
		} else if i%109 == 0 {
			lines = append(lines, fmt.Sprintf("%s WARN collector slow check=%s duration_ms=%d recovered=true token=container-agent-noise-%04d", isoTime(dt), checks[i%len(checks)], 250+i%80, i))
		} else if i%173 == 0 {
			lines = append(lines, fmt.Sprintf("%s ERROR logs-agent tailer transient error file=/var/log/pods/noisy.log error=\"file rotated\" recovered=true token=container-agent-noise-%04d", isoTime(dt), i))
		} else {
			lines = append(lines, fmt.Sprintf("%s DEBUG collector heartbeat check=%s status=OK sequence=%04d token=container-agent-noise", isoTime(dt), checks[i%len(checks)], i))
		}
	}
	return lines
}

func generateContainerSyslog() []string {
	start := time.Date(2026, 4, 30, 11, 0, 0, 0, time.UTC)
	events := map[int]string{
		4:   "node systemd[1]: Started Datadog Agent container fixture.",
		116: "node chronyd[801]: Selected source 192.0.2.10 (time.example) but system clock is unsynchronised",
		128: "node kernel: clocksource: timekeeping watchdog on CPU0: Marking clocksource tsc as unstable because the skew is too large",
		132: "node chronyd[801]: System clock wrong by 07:53:46.217 seconds; waiting for makestep window",
		134: "node datadog-agent[17]: kubernetes_apiserver check failing: x509 certificate is not yet valid (agent clock before certificate NotBefore)",
		240: "node kubelet[22]: certificate rotation pending approval for client kubelet; unrelated to apiserver serving cert",
		256: "node chronyd[801]: System clock was stepped by +28426.217 seconds to correct skew",
		262: "node kubelet[22]: Node clock synchronized after chrony step",
		300: "node datadog-agent[17]: kubernetes_apiserver check retry still failing until next collector interval",
		420: "node datadog-agent[17]: kubernetes_apiserver check recovered after clock synchronization status=OK",
	}

	lines := make([]string, 0, 750)
	for i := 0; i < 750; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", syslogTime(dt), event))
		} else if i%127 == 0 {
			lines = append(lines, fmt.Sprintf("%s node kubelet[22]: pod sandbox changed pod=fixture-noise-%d namespace=default", syslogTime(dt), i))
		} else if i%89 == 0 {
			lines = append(lines, fmt.Sprintf("%s node containerd[33]: image garbage collection completed reclaimed=%dMB token=container-syslog-noise-%04d", syslogTime(dt), i%17, i))
		} else {
			lines = append(lines, fmt.Sprintf("%s node systemd[1]: fixture heartbeat unit=container-runtime.service sequence=%04d token=container-syslog-noise", syslogTime(dt), i))
		}
	}
	return lines
}

func generateHoldoutCheckoutLog() []string {
	start := time.Date(2026, 5, 1, 14, 15, 0, 0, time.UTC)
	events := map[int]string{
		0:   "INFO service=checkout boot complete version=2026.05.01 build=holdout-a config_source=file",
		312: "INFO service=checkout postgres health status=OK pool=checkout_rw active=42 idle=18 max=120 latency_ms=15",
		396: "WARN service=checkout dependency latency high dependency=payments route=/api/pay p95_ms=1800 request_id=pay-2198",
		414: "ERROR service=checkout request failed id=pay-2201 route=/api/pay status=502 upstream=payments error=\"lookup payments.service.consul: no such host\" resolver=10.0.0.53",
		421: "ERROR service=checkout request failed id=pay-2202 route=/api/pay status=502 upstream=payments error=\"dial tcp: lookup payments.service.consul: i/o timeout\" resolver=10.0.0.53",
		427: "WARN service=checkout circuit breaker opened dependency=payments reason=\"dns resolution failure\" window=60s",
		456: "INFO service=checkout postgres health status=OK pool=checkout_rw active=43 idle=17 max=120 latency_ms=17",
		509: "ERROR service=checkout request failed id=pay-2211 route=/api/pay status=502 upstream=payments error=\"lookup payments.service.consul: server misbehaving\" resolver=10.0.0.53",
		690: "INFO service=checkout dependency=payments recovered status=OK dns_cache_refreshed=true",
	}
	routes := []string{"/api/cart", "/api/pay", "/api/profile", "/health"}
	lines := make([]string, 0, 760)
	for i := 0; i < 760; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", isoTime(dt), event))
			continue
		}
		route := routes[(i*3)%len(routes)]
		if i%173 == 0 {
			lines = append(lines, fmt.Sprintf("%s WARN service=checkout db pool wait elevated pool=analytics_ro active=%d max=20 recovered=true note=\"old analytics noise, not checkout_rw\"", isoTime(dt), 12+i%5))
		} else if i%137 == 0 {
			lines = append(lines, fmt.Sprintf("%s ERROR service=checkout feature flag refresh failed flag=upsell recovered=true token=holdout-checkout-noise-%04d", isoTime(dt), i))
		} else {
			lines = append(lines, fmt.Sprintf("%s INFO service=checkout handled request id=pay-noise-%04d route=%s status=200 latency_ms=%d token=holdout-checkout", isoTime(dt), i, route, 30+i%120))
		}
	}
	return lines
}

func generateHoldoutNginxAccessLog() []string {
	start := time.Date(2026, 5, 1, 14, 10, 0, 0, time.UTC)
	failures := map[int]int{720: 502, 724: 502, 729: 502, 736: 502, 741: 502, 748: 502, 756: 502, 768: 502}
	lines := make([]string, 0, 1050)
	for i := 0; i < 1050; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		client := fmt.Sprintf("198.51.100.%d", 40+i%40)
		if code, ok := failures[i]; ok {
			lines = append(lines, fmt.Sprintf("%s - - [%s] \"POST /api/pay HTTP/1.1\" %d 173 \"-\" \"holdout-client/%d\" request_id=pay-%04d", client, nginxTime(dt), code, i%5, 2200+i-720))
		} else if i%211 == 0 {
			lines = append(lines, fmt.Sprintf("%s - - [%s] \"GET /api/search?q=noise HTTP/1.1\" 500 211 \"-\" \"holdout-client/%d\" request_id=search-holdout-%04d", client, nginxTime(dt), i%5, i))
		} else {
			route := "/api/cart"
			method := "GET"
			if i%4 == 0 {
				route = "/api/pay"
				method = "POST"
			}
			lines = append(lines, fmt.Sprintf("%s - - [%s] \"%s %s HTTP/1.1\" 200 %d \"-\" \"holdout-client/%d\" request_id=pay-noise-%04d", client, nginxTime(dt), method, route, 300+i%400, i%5, i))
		}
	}
	return lines
}

func generateHoldoutSystemLog() []string {
	start := time.Date(2026, 5, 1, 14, 15, 0, 0, time.UTC)
	events := map[int]string{
		378: "edge systemd-resolved[511]: DNS server 10.0.0.53 timed out, retrying transaction=payments.service.consul type=A",
		414: "edge systemd-resolved[511]: Server returned error SERVFAIL for payments.service.consul IN A",
		418: "edge dnsmasq[902]: query[A] payments.service.consul from 10.0.12.44",
		419: "edge dnsmasq[902]: forwarded payments.service.consul to 10.0.0.53",
		420: "edge dnsmasq[902]: reply payments.service.consul is SERVFAIL",
		456: "edge postgres[2300]: LOG: checkpoint complete: wrote 48 buffers; connections active=43 max=120",
		691: "edge systemd-resolved[511]: DNS lookup for payments.service.consul recovered status=NOERROR ttl=30",
	}
	lines := make([]string, 0, 760)
	for i := 0; i < 760; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", syslogTime(dt), event))
		} else if i%149 == 0 {
			lines = append(lines, fmt.Sprintf("%s edge kernel: audit: type=1400 apparmor=\"DENIED\" operation=\"open\" profile=\"fixture\" name=\"/tmp/holdout-noise-%d\"", syslogTime(dt), i))
		} else {
			lines = append(lines, fmt.Sprintf("%s edge systemd[1]: fixture heartbeat unit=checkout.slice sequence=%04d token=holdout-system", syslogTime(dt), i))
		}
	}
	return lines
}

func generateHoldoutWorkerLog() []string {
	start := time.Date(2026, 5, 1, 15, 58, 0, 0, time.UTC)
	events := map[int]string{
		0:   "INFO service=async-worker boot complete version=2026.05.01 build=77ac21 pid=4441",
		215: "WARN service=async-worker heartbeat delayed queue=emails lag_ms=2100 recovered=true",
		300: "INFO service=async-worker received signal signal=SIGTERM pid=4441 reason=unknown drain_started=true",
		303: "INFO service=async-worker shutdown complete pid=4441 jobs_inflight=0 exit_code=0",
		316: "INFO service=async-worker boot complete version=2026.05.01 build=77ac21 pid=4528 note=\"same build after restart\"",
		410: "INFO service=async-worker queue healthy queue=emails lag_ms=88",
	}
	lines := make([]string, 0, 620)
	for i := 0; i < 620; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", isoTime(dt), event))
		} else if i%181 == 0 {
			lines = append(lines, fmt.Sprintf("%s ERROR service=async-worker email provider transient timeout recovered=true token=worker-noise-%04d", isoTime(dt), i))
		} else {
			lines = append(lines, fmt.Sprintf("%s DEBUG service=async-worker heartbeat pid=4441 sequence=%04d token=worker-holdout", isoTime(dt), i))
		}
	}
	return lines
}

func generateHoldoutAuthLog() []string {
	start := time.Date(2026, 5, 1, 15, 45, 0, 0, time.UTC)
	events := map[int]string{
		240: "ops sshd[3100]: Accepted publickey for deploy from 203.0.113.42 port 61022 ssh2: ED25519 SHA256:holdout-deploy",
		312: "ops sudo:   deploy : TTY=pts/1 ; PWD=/srv/app ; USER=root ; COMMAND=/usr/bin/systemctl status async-worker.service",
		780: "ops sudo:   deploy : TTY=pts/1 ; PWD=/srv/app ; USER=root ; COMMAND=/usr/bin/journalctl -u async-worker -n 50",
	}
	lines := make([]string, 0, 980)
	for i := 0; i < 980; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", syslogTime(dt), event))
		} else if i%197 == 0 {
			lines = append(lines, fmt.Sprintf("%s ops sshd[%d]: Failed password for invalid user temp from 198.51.100.%d port %d ssh2", syslogTime(dt), 4200+i, 80+i%10, 50000+i))
		} else {
			lines = append(lines, fmt.Sprintf("%s ops CRON[%d]: pam_unix(cron:session): session closed for user root token=holdout-auth-%04d", syslogTime(dt), 5000+i, i))
		}
	}
	return lines
}

func generateHoldoutDeployLog() []string {
	start := time.Date(2026, 5, 1, 15, 0, 0, 0, time.UTC)
	events := map[int]string{
		10:  "INFO deploy id=dep-771 service=async-worker version=2026.05.01 started_by=release-bot",
		132: "INFO deploy id=dep-771 service=async-worker version=2026.05.01 completed status=success finished_at=2026-05-01T15:02:12Z",
		540: "INFO deploy controller heartbeat service=checkout no_change=true",
	}
	lines := make([]string, 0, 620)
	for i := 0; i < 620; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", isoTime(dt), event))
		} else {
			lines = append(lines, fmt.Sprintf("%s DEBUG deploy controller idle sequence=%04d token=holdout-deploy", isoTime(dt), i))
		}
	}
	return lines
}

func generateHoldoutAuthSuccessLog() []string {
	start := time.Date(2026, 5, 1, 18, 10, 0, 0, time.UTC)
	users := []string{"backup", "deploy", "oracle", "postgres", "admin", "mysql", "support"}
	failures := map[int]int{}
	for n := 0; n < 42; n++ {
		failures[180+n*5] = n
	}
	events := map[int]string{
		92:  "login01 sshd[6120]: Accepted publickey for release from 198.51.100.44 port 61192 ssh2: ED25519 SHA256:holdout-release",
		176: "login01 sshd[6220]: Invalid user backup from 203.0.113.66 port 54101",
		402: "login01 sshd[6801]: maximum authentication attempts exceeded for invalid user support from 203.0.113.66 port 54380 ssh2 [preauth]",
		428: "login01 sshd[6810]: Accepted password for backup from 203.0.113.66 port 54402 ssh2",
		430: "login01 sshd[6810]: pam_unix(sshd:session): session opened for user backup(uid=1007) by (uid=0)",
		492: "login01 sudo:   backup : TTY=pts/2 ; PWD=/home/backup ; USER=root ; COMMAND=/usr/bin/id",
		710: "login01 sshd[6910]: Accepted publickey for deploy from 203.0.113.42 port 61222 ssh2: RSA SHA256:holdout-deploy",
	}
	lines := make([]string, 0, 900)
	for i := 0; i < 900; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if n, ok := failures[i]; ok {
			user := users[n%len(users)]
			lines = append(lines, fmt.Sprintf("%s login01 sshd[%d]: Failed password for invalid user %s from 203.0.113.66 port %d ssh2", syslogTime(dt), 6500+n, user, 54100+n))
		} else if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", syslogTime(dt), event))
		} else if i%157 == 0 {
			lines = append(lines, fmt.Sprintf("%s login01 sshd[%d]: Failed password for invalid user temp from 198.51.100.%d port %d ssh2", syslogTime(dt), 7000+i, 90+i%10, 51000+i))
		} else {
			lines = append(lines, fmt.Sprintf("%s login01 CRON[%d]: pam_unix(cron:session): session closed for user root token=holdout-auth-success-%04d", syslogTime(dt), 8000+i, i))
		}
	}
	return lines
}

func generateHoldoutDatadogAPIKeyLog() []string {
	start := time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC)
	events := map[int]string{
		0:   "INFO agent starting version=7.99.0 build=holdout-api host=api-01 remote_config=true",
		48:  "INFO config loaded from /etc/datadog-agent/datadog.yaml source=file status=OK",
		121: "INFO remote config poll complete transaction_id=rc-9901 changed=false products=agent_config,apm_sampling",
		296: "INFO forwarder domain=series endpoint=/api/v1/series status=202 payloads_sent=42",
		367: "ERROR api key validation failed key_id=ak-2209 endpoint=/api/v1/validate status=403 reason=api_key_invalid",
		371: "ERROR forwarder dropping metric payload domain=series endpoint=/api/v1/series status=403 error=invalid_api_key retryable=false",
		374: "ERROR logs intake rejected domain=logs endpoint=/api/v2/logs status=403 error=invalid_api_key",
		382: "WARN trace-agent intake rejected endpoint=/api/v0.2/traces status=403 reason=invalid_api_key",
		428: "WARN no metrics flushed since 2026-05-01T11:06:11Z reason=api_key_invalid",
		536: "INFO remote config poll complete transaction_id=rc-9902 changed=false products=agent_config,apm_sampling",
		690: "WARN no config validation errors observed since boot status=OK note=api-key-failure-not-yaml",
	}
	checks := []string{"cpu", "disk", "network", "postgres", "container", "redisdb"}
	lines := make([]string, 0, 900)
	for i := 0; i < 900; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", isoTime(dt), event))
		} else if i%127 == 0 {
			lines = append(lines, fmt.Sprintf("%s WARN collector transient check=%s error=timeout recovered=true token=api-agent-noise-%04d", isoTime(dt), checks[i%len(checks)], i))
		} else if i%53 == 0 {
			lines = append(lines, fmt.Sprintf("%s DEBUG remote config poll skipped jitter_ms=%d transaction_id=rc-noop-api-%04d", isoTime(dt), 80+i%300, i))
		} else {
			lines = append(lines, fmt.Sprintf("%s DEBUG collector heartbeat check=%s status=OK sequence=%04d token=api-agent-holdout", isoTime(dt), checks[(i*5)%len(checks)], i))
		}
	}
	return lines
}

func generateHoldoutCartLog() []string {
	start := time.Date(2026, 5, 1, 17, 40, 0, 0, time.UTC)
	events := map[int]string{
		0:   "INFO service=cart boot complete version=2026.05.01 build=cart-901",
		226: "INFO service=cart postgres health status=OK pool=cart_rw active=31 idle=24 max=100 latency_ms=12",
		303: "WARN service=cart cache latency high dependency=redis p95_ms=1450 route=/api/cart request_id=cart-7781",
		318: "ERROR service=cart request failed id=cart-7784 route=/api/cart status=503 dependency=redis error=\"ERR max number of clients reached\" cache=cart-primary",
		324: "ERROR service=cart request failed id=cart-7785 route=/api/cart status=503 dependency=redis error=\"dial tcp 10.2.4.19:6379: connection refused\" cache=cart-primary",
		337: "WARN service=cart circuit breaker opened dependency=redis reason=cache dependency failure window=60s",
		402: "INFO service=cart postgres health status=OK pool=cart_rw active=32 idle=25 max=100 latency_ms=11",
		511: "INFO service=cart cache dependency recovered dependency=redis status=OK reconnects=3",
	}
	routes := []string{"/api/cart", "/api/profile", "/api/promotions", "/health"}
	lines := make([]string, 0, 780)
	for i := 0; i < 780; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", isoTime(dt), event))
		} else if i%149 == 0 {
			lines = append(lines, fmt.Sprintf("%s WARN service=cart payment enrichment timeout recovered=true token=cart-noise-%04d", isoTime(dt), i))
		} else {
			lines = append(lines, fmt.Sprintf("%s INFO service=cart handled request id=cart-noise-%04d route=%s status=200 latency_ms=%d token=cart-holdout", isoTime(dt), i, routes[(i*7)%len(routes)], 20+i%70))
		}
	}
	return lines
}

func generateHoldoutCartRotatedLog() []string {
	start := time.Date(2026, 4, 30, 17, 30, 0, 0, time.UTC)
	// Previous-day (2026-04-30) DB pool exhaustion incident on the cart
	// service. The current cart.log incident is on 2026-05-01 and is driven
	// by Redis maxclients, not Postgres. The model must use the date and the
	// dependency (redis vs postgres) to disambiguate; no "old_incident" or
	// "previous-day-noise" labels are provided as hints.
	events := map[int]string{
		112: "WARN service=cart db pool wait high pool=cart_rw active=98 max=100 wait_ms=500 recovered=true",
		141: "ERROR service=cart db pool exhausted pool=cart_rw active=100 max=100 suspected_client=reporting-worker",
		154: "ERROR service=cart request failed id=cart-old-331 route=/api/cart status=500 error=\"pq: remaining connection slots are reserved\"",
		260: "INFO service=cart database recovered pool=cart_rw active=37 max=100",
	}
	lines := make([]string, 0, 620)
	for i := 0; i < 620; i++ {
		dt := start.Add(time.Duration(i*2) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", isoTime(dt), event))
		} else {
			lines = append(lines, fmt.Sprintf("%s DEBUG service=cart previous-rotation heartbeat sequence=%04d token=cart-rotated-holdout", isoTime(dt), i))
		}
	}
	return lines
}

func generateHoldoutCartNginxAccessLog() []string {
	start := time.Date(2026, 5, 1, 17, 39, 0, 0, time.UTC)
	failures := map[int]int{360: 503, 363: 503, 369: 503, 376: 503, 384: 503, 397: 503, 411: 503}
	lines := make([]string, 0, 900)
	for i := 0; i < 900; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		client := fmt.Sprintf("198.51.100.%d", 20+i%60)
		if code, ok := failures[i]; ok {
			lines = append(lines, fmt.Sprintf("%s - - [%s] \"GET /api/cart HTTP/1.1\" %d 189 \"-\" \"holdout-cart/%d\" request_id=cart-%04d", client, nginxTime(dt), code, i%4, 7780+i-360))
		} else if i%223 == 0 {
			lines = append(lines, fmt.Sprintf("%s - - [%s] \"GET /api/search HTTP/1.1\" 502 144 \"-\" \"holdout-cart/%d\" request_id=search-noise-%04d", client, nginxTime(dt), i%4, i))
		} else {
			lines = append(lines, fmt.Sprintf("%s - - [%s] \"GET /api/cart HTTP/1.1\" 200 %d \"-\" \"holdout-cart/%d\" request_id=cart-noise-%04d", client, nginxTime(dt), 250+i%300, i%4, i))
		}
	}
	return lines
}

func generateHoldoutCartSystemLog() []string {
	start := time.Date(2026, 5, 1, 17, 40, 0, 0, time.UTC)
	events := map[int]string{
		292: "cart-node redis-server[771]: clients nearing maxclients connected_clients=998 maxclients=1000",
		318: "cart-node redis-server[771]: ERR max number of clients reached client=10.2.4.55:44821",
		322: "cart-node kernel: TCP: request_sock_TCP: Possible SYN flooding on port 6379. Sending cookies. Check SNMP counters.",
		405: "cart-node postgres[2400]: LOG: connection count active=32 max=100 database=cart status=OK",
		512: "cart-node redis-server[771]: clients below maxclients connected_clients=212 maxclients=1000 recovered=true",
	}
	lines := make([]string, 0, 760)
	for i := 0; i < 760; i++ {
		dt := start.Add(time.Duration(i) * time.Second)
		if event, ok := events[i]; ok {
			lines = append(lines, fmt.Sprintf("%s %s", syslogTime(dt), event))
		} else if i%181 == 0 {
			lines = append(lines, fmt.Sprintf("%s cart-node kernel: audit: type=1400 apparmor=\"DENIED\" operation=\"open\" profile=\"fixture\" token=cart-system-noise-%04d", syslogTime(dt), i))
		} else {
			lines = append(lines, fmt.Sprintf("%s cart-node systemd[1]: fixture heartbeat unit=cart.slice sequence=%04d token=cart-system-holdout", syslogTime(dt), i))
		}
	}
	return lines
}
