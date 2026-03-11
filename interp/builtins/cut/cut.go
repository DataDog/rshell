// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package cut implements the cut builtin command.
//
// cut — remove sections from each line of files
//
// Usage: cut OPTION... [FILE]...
//
// Print selected parts of lines from each FILE to standard output.
// With no FILE, or when FILE is -, read standard input.
//
// Exactly one of -b, -c, or -f must be specified.
//
// Accepted flags:
//
//	-b LIST, --bytes=LIST
//	    Select only these bytes. LIST is a comma-separated set of byte
//	    positions and ranges (e.g. 1,3-5,7-). Positions are 1-based.
//
//	-c LIST, --characters=LIST
//	    Select only these characters. Same list format as -b.
//
//	-d DELIM, --delimiter=DELIM
//	    Use DELIM instead of TAB for field delimiter. Used with -f.
//
//	-f LIST, --fields=LIST
//	    Select only these fields, separated by the delimiter character.
//	    Same list format as -b.
//
//	-n
//	    Do not split multi-byte characters (used with -b). Byte ranges
//	    are adjusted to avoid cutting in the middle of a character.
//
//	-s, --only-delimited
//	    Do not print lines not containing delimiters (only with -f).
//
//	--complement
//	    Complement the set of selected bytes, characters, or fields.
//
//	--output-delimiter=STRING
//	    Use STRING as the output delimiter. The default is the input
//	    delimiter.
//
//	-h, --help
//	    Print this usage message to stdout and exit 0.
//
// Exit codes:
//
//	0  All files processed successfully.
//	1  At least one error occurred (missing file, invalid argument, etc.).
//
// Memory safety:
//
//	Lines are read via a streaming scanner with a per-line cap of
//	MaxLineBytes (1 MiB). Lines exceeding this cap produce an error
//	rather than an unbounded allocation. All loops check ctx.Err()
//	at each iteration to honour the shell's execution timeout.
package cut
