// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package etcpasswd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// etcPasswdPath is the file read by userExistsImpl. It is hardcoded — never
// derived from user input — so it is exempt from the AllowedPaths sandbox.
// See the package doc comment for the rationale.
const etcPasswdPath = "/etc/passwd"

// maxPasswdLine caps the per-line buffer size when scanning /etc/passwd.
// Real lines are well under a few KiB; this is a generous bound against a
// pathological /etc/passwd.
const maxPasswdLine = 64 * 1024

// maxPasswdLines caps the total number of lines scanned, bounding CPU time
// on a pathological input with many short lines.
const maxPasswdLines = 1_000_000

// userExistsImpl opens and scans /etc/passwd for name.
func userExistsImpl(name string) (bool, error) {
	f, err := os.Open(etcPasswdPath)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", etcPasswdPath, err)
	}
	defer f.Close() //nolint:errcheck

	found, err := parsePasswdFile(f, name)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", etcPasswdPath, err)
	}
	return found, nil
}

// parsePasswdFile scans /etc/passwd-formatted lines from r looking for an
// account named name. Split out from userExistsImpl so tests can exercise
// the parsing logic directly against a strings.Reader instead of the real
// /etc/passwd.
//
// Each non-comment, non-empty line is expected in the standard
// "name:passwd:uid:gid:gecos:home:shell" form. Lines that don't even have
// the name field followed by a colon are skipped rather than treated as
// fatal, matching glibc's tolerant nss_files behaviour and this package's
// sibling etcgroup.
func parsePasswdFile(r io.Reader, name string) (bool, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024), maxPasswdLine)

	lines := 0
	for scanner.Scan() {
		lines++
		if lines > maxPasswdLines {
			break
		}
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		userName, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if userName == name {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}
