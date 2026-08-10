// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package jq

import (
	"errors"
	"io"
)

var (
	errInvalidSurrogate = errors.New("invalid Unicode surrogate escape")
	errInvalidUTF8      = errors.New("invalid UTF-8 input")
)

// surrogateValidator rejects lone UTF-16 surrogate escapes before
// encoding/json replaces them with U+FFFD. State is retained across reads so a
// pair split at an arbitrary input chunk boundary is still validated.
type surrogateValidator struct {
	reader io.Reader

	inString    bool
	escaped     bool
	digitsLeft  int
	codeUnit    uint16
	lowUnit     bool
	pendingHigh bool
	needLowU    bool
	utf8Need    int
	utf8Min     byte
	utf8Max     byte
}

func (v *surrogateValidator) Read(p []byte) (int, error) {
	n, err := v.reader.Read(p)
	if scanErr := v.scan(p[:n]); scanErr != nil {
		return 0, scanErr
	}
	if errors.Is(err, io.EOF) {
		if v.utf8Need != 0 {
			return 0, errInvalidUTF8
		}
		if v.pendingHigh || v.needLowU || v.lowUnit {
			return 0, errInvalidSurrogate
		}
	}
	return n, err
}

func (v *surrogateValidator) scan(data []byte) error {
	for _, c := range data {
		if err := v.scanUTF8(c); err != nil {
			return err
		}
		if !v.inString {
			if c == '"' {
				v.inString = true
			}
			continue
		}
		if v.digitsLeft > 0 {
			digit, ok := hexDigit(c)
			if !ok {
				// encoding/json will report the malformed hexadecimal escape.
				v.digitsLeft = 0
				v.lowUnit = false
				continue
			}
			v.codeUnit = v.codeUnit<<4 | digit
			v.digitsLeft--
			if v.digitsLeft == 0 {
				if v.lowUnit {
					if v.codeUnit < 0xdc00 || v.codeUnit > 0xdfff {
						return errInvalidSurrogate
					}
					v.lowUnit = false
				} else if v.codeUnit >= 0xd800 && v.codeUnit <= 0xdbff {
					v.pendingHigh = true
				} else if v.codeUnit >= 0xdc00 && v.codeUnit <= 0xdfff {
					return errInvalidSurrogate
				}
			}
			continue
		}
		if v.needLowU {
			if c != 'u' {
				return errInvalidSurrogate
			}
			v.needLowU = false
			v.lowUnit = true
			v.digitsLeft = 4
			v.codeUnit = 0
			continue
		}
		if v.pendingHigh {
			if c != '\\' {
				return errInvalidSurrogate
			}
			v.pendingHigh = false
			v.needLowU = true
			continue
		}
		if v.escaped {
			v.escaped = false
			if c == 'u' {
				v.digitsLeft = 4
				v.codeUnit = 0
			}
			continue
		}
		switch c {
		case '\\':
			v.escaped = true
		case '"':
			v.inString = false
		}
	}
	return nil
}

func (v *surrogateValidator) scanUTF8(c byte) error {
	if v.utf8Need > 0 {
		if c < v.utf8Min || c > v.utf8Max {
			return errInvalidUTF8
		}
		v.utf8Need--
		v.utf8Min, v.utf8Max = 0x80, 0xbf
		return nil
	}
	switch {
	case c < 0x80:
		return nil
	case c >= 0xc2 && c <= 0xdf:
		v.utf8Need, v.utf8Min, v.utf8Max = 1, 0x80, 0xbf
	case c == 0xe0:
		v.utf8Need, v.utf8Min, v.utf8Max = 2, 0xa0, 0xbf
	case c >= 0xe1 && c <= 0xec || c >= 0xee && c <= 0xef:
		v.utf8Need, v.utf8Min, v.utf8Max = 2, 0x80, 0xbf
	case c == 0xed:
		v.utf8Need, v.utf8Min, v.utf8Max = 2, 0x80, 0x9f
	case c == 0xf0:
		v.utf8Need, v.utf8Min, v.utf8Max = 3, 0x90, 0xbf
	case c >= 0xf1 && c <= 0xf3:
		v.utf8Need, v.utf8Min, v.utf8Max = 3, 0x80, 0xbf
	case c == 0xf4:
		v.utf8Need, v.utf8Min, v.utf8Max = 3, 0x80, 0x8f
	default:
		return errInvalidUTF8
	}
	return nil
}

func validateSurrogates(text string) error {
	v := &surrogateValidator{}
	if err := v.scan([]byte(text)); err != nil {
		return err
	}
	if v.utf8Need != 0 {
		return errInvalidUTF8
	}
	if v.pendingHigh || v.needLowU || v.lowUnit {
		return errInvalidSurrogate
	}
	return nil
}

func hexDigit(c byte) (uint16, bool) {
	switch {
	case c >= '0' && c <= '9':
		return uint16(c - '0'), true
	case c >= 'a' && c <= 'f':
		return uint16(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return uint16(c-'A') + 10, true
	default:
		return 0, false
	}
}
