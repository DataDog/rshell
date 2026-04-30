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

	files := []struct {
		path  string
		lines []string
	}{
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
	}

	for _, file := range files {
		if err := writeFixtureLines(filepath.Join(fixtureRoot, filepath.FromSlash(file.path)), file.lines); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(fixtureRoot, "container", "var", "log"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(fixtureRoot, "container", "var", "log", ".gitkeep"), nil, 0o644)
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
		1120: "INFO trace-agent heartbeat status=OK spans_sent=293 note=\"red herring: traces unaffected\"",
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
	events := map[int]string{
		14:  "INFO agent starting version=7.98.1 build=fixture host=checkout-01",
		119: "ERROR config validation failed file=/etc/datadog-agent/conf.d/http_check.d/conf.yaml line=17 error=\"missing required field url\" check=http_check recovered=true",
		131: "INFO collector check recovered check=http_check status=OK after_fix=true",
		311: "WARN forwarder retryable error domain=logs endpoint=/api/v2/logs status=503 retry_in=15s recovered=true",
		325: "INFO forwarder recovered domain=logs endpoint=/api/v2/logs status=202",
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
		777: "WARN service=checkout cache miss spike cache=redis route=/api/cart note=\"not correlated with checkout 500s\"",
		910: "INFO service=checkout payment gateway status=OK note=\"red herring resolved before incident\"",
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
			lines = append(lines, fmt.Sprintf("%s INFO service=checkout recovered route=/api/checkout status=200 note=\"old rotation red herring\"", isoTime(dt)))
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
			lines = append(lines, fmt.Sprintf("%s host postgres[1200]: FATAL: password authentication failed for user \"readonly\" recovered=true old_rotation=true", syslogTime(dt)))
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
		512: "INFO collector check completed check=container status=OK latency_ms=22 note=\"red herring: container check healthy\"",
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
