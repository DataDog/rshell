// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package etcgroup

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// etcGroupPath is the file read by lookupGIDImpl. It is hardcoded — never
// derived from user input — so it is exempt from the AllowedPaths sandbox.
// See the package doc comment for the rationale.
const etcGroupPath = "/etc/group"

// maxGroupLine caps the per-line buffer size when scanning /etc/group. Real
// lines (even with a long member list) are well under a few KiB; this is a
// generous bound against a pathological /etc/group.
const maxGroupLine = 64 * 1024

// maxGroupLines caps the total number of lines scanned, bounding CPU time on
// a pathological input with many short lines.
const maxGroupLines = 1_000_000

// lookupGIDImpl opens and scans /etc/group for name.
func lookupGIDImpl(name string) (uint32, error) {
	f, err := os.Open(etcGroupPath)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", etcGroupPath, err)
	}
	defer f.Close() //nolint:errcheck

	gid, err := parseGroupFile(f, name)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", etcGroupPath, err)
	}
	return gid, nil
}

// parseGroupFile scans /etc/group-formatted lines from r looking for a
// group named name, returning its GID. Split out from lookupGIDImpl so
// tests can exercise the parsing logic directly against a strings.Reader
// instead of the real /etc/group.
//
// Each non-comment, non-empty line is expected in the standard
// "name:passwd:gid:members" form. Lines that don't parse cleanly are
// skipped rather than treated as fatal, matching glibc's tolerant
// nss_files behaviour.
func parseGroupFile(r io.Reader, name string) (uint32, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024), maxGroupLine)

	lines := 0
	for scanner.Scan() {
		lines++
		if lines > maxGroupLines {
			break
		}
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		groupName, rest, ok := strings.Cut(line, ":")
		if !ok || groupName != name {
			continue
		}
		_, gidField, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		gidStr, _, _ := strings.Cut(gidField, ":")
		gid, err := strconv.ParseUint(gidStr, 10, 32)
		if err != nil {
			continue
		}
		return uint32(gid), nil
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, ErrGroupNotFound
}
