// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package jq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Resource limits are intentionally local to one jq invocation. They keep
// parsing, evaluation, and serialization bounded even when jq reads from a
// device or another command that never reaches EOF.
const (
	MaxFilterBytes     = 64 << 10
	MaxTotalInputBytes = 8 << 20
	MaxRawLineBytes    = 1 << 20
	MaxNestingDepth    = 64
	MaxFilterNodes     = 4_096
	MaxValueNodes      = 65_536
	MaxValueBytes      = 4 << 20
	MaxIntegerBits     = 256
	MaxEvalSteps       = 100_000
	MaxResults         = 10_000
	MaxResultNodes     = 2 * MaxValueNodes
	MaxResultBytes     = 8 << 20
	MaxOutputBytes     = 1 << 20

	maxNumberBytes = 256
)

var (
	errIntegerRange = errors.New("integer exceeds the supported 256-bit range")
	errValueNodes   = errors.New("JSON value contains too many nodes")
	errValueBytes   = errors.New("JSON value exceeds the size limit")
	errValueDepth   = errors.New("JSON value is nested too deeply")
	errOutputLimit  = errors.New("generated output exceeds the size limit")
)

type valueKind uint8

const (
	valueNull valueKind = iota
	valueBoolean
	valueNumber
	valueString
	valueArray
	valueObject
)

type number struct {
	integer *big.Int
	float   float64
}

func (n number) isInteger() bool { return n.integer != nil }

func (n number) indexInt64() (int64, bool) {
	if n.isInteger() {
		if !n.integer.IsInt64() {
			return 0, false
		}
		return n.integer.Int64(), true
	}
	truncated := math.Trunc(n.float)
	if truncated < float64(math.MinInt64) || truncated >= -float64(math.MinInt64) {
		return 0, false
	}
	return int64(truncated), true
}

func (n number) text() string {
	if n.integer != nil {
		return n.integer.String()
	}
	return strconv.FormatFloat(n.float, 'g', -1, 64)
}

type objectMember struct {
	key   string
	value value
}

type objectValue struct {
	members []objectMember
	index   map[string]int
}

type value struct {
	kind  valueKind
	bool  bool
	num   number
	str   string
	array []value
	obj   objectValue
	nodes int
	bytes int
	depth int
}

func null() value {
	return value{kind: valueNull, nodes: 1, bytes: 4}
}

func boolean(b bool) value {
	size := 5
	if b {
		size = 4
	}
	return value{kind: valueBoolean, bool: b, nodes: 1, bytes: size}
}

func stringValue(s string) (value, error) {
	s = normalizeUTF8(s)
	if len(s)+2 > MaxValueBytes {
		return value{}, errValueBytes
	}
	return value{kind: valueString, str: s, nodes: 1, bytes: len(s) + 2}, nil
}

func normalizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var output strings.Builder
	output.Grow(len(s))
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size == 1 {
			output.WriteRune(utf8.RuneError)
			s = s[1:]
			continue
		}
		output.WriteString(s[:size])
		s = s[size:]
	}
	return output.String()
}

func integerValue(i *big.Int) (value, error) {
	if i == nil || i.BitLen() > MaxIntegerBits {
		return value{}, errIntegerRange
	}
	copyInt := new(big.Int).Set(i)
	text := copyInt.String()
	return value{
		kind:  valueNumber,
		num:   number{integer: copyInt},
		nodes: 1,
		bytes: len(text),
	}, nil
}

func int64Value(i int64) value {
	v, _ := integerValue(big.NewInt(i))
	return v
}

func addAggregate(nodes, size *int, item value, extra, maxNodes, maxBytes int) error {
	if item.nodes > maxNodes-*nodes {
		return errValueNodes
	}
	if extra > maxBytes || item.bytes > maxBytes-extra || *size > maxBytes-extra-item.bytes {
		return errValueBytes
	}
	*nodes += item.nodes
	*size += extra + item.bytes
	return nil
}

func floatValue(f float64) (value, error) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return value{}, errors.New("number is not finite")
	}
	text := strconv.FormatFloat(f, 'g', -1, 64)
	return value{
		kind:  valueNumber,
		num:   number{float: f},
		nodes: 1,
		bytes: len(text),
	}, nil
}

func parseNumber(text string) (value, error) {
	if len(text) == 0 || len(text) > maxNumberBytes {
		return value{}, errors.New("number literal is too long")
	}
	if strings.ContainsAny(text, ".eE") {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return value{}, fmt.Errorf("invalid number %q", text)
		}
		return floatValue(f)
	}
	i, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return value{}, fmt.Errorf("invalid number %q", text)
	}
	return integerValue(i)
}

func arrayValue(items []value) (value, error) {
	nodes, size, depth := 1, 2, 1
	for i := range items {
		separator := 0
		if i > 0 {
			separator = 1
		}
		if err := addAggregate(&nodes, &size, items[i], separator, MaxValueNodes, MaxValueBytes); err != nil {
			return value{}, err
		}
		if items[i].depth+1 > depth {
			depth = items[i].depth + 1
		}
	}
	if depth > MaxNestingDepth {
		return value{}, errValueDepth
	}
	return value{kind: valueArray, array: items, nodes: nodes, bytes: size, depth: depth}, nil
}

func object(items []objectMember) (value, error) {
	members := make([]objectMember, 0, len(items))
	index := make(map[string]int, len(items))
	for _, item := range items {
		if existing, ok := index[item.key]; ok {
			members[existing].value = item.value
			continue
		}
		index[item.key] = len(members)
		members = append(members, item)
	}

	nodes, size, depth := 1, 2, 1
	for i := range members {
		item := &members[i]
		separator := 0
		if i > 0 {
			separator = 1
		}
		if err := addAggregate(&nodes, &size, item.value, len(item.key)+3+separator, MaxValueNodes, MaxValueBytes); err != nil {
			return value{}, err
		}
		if item.value.depth+1 > depth {
			depth = item.value.depth + 1
		}
	}
	if depth > MaxNestingDepth {
		return value{}, errValueDepth
	}

	return value{
		kind:  valueObject,
		obj:   objectValue{members: members, index: index},
		nodes: nodes,
		bytes: size,
		depth: depth,
	}, nil
}

func (o objectValue) get(key string) (value, bool) {
	i, ok := o.index[key]
	if !ok {
		return value{}, false
	}
	return o.members[i].value, true
}

type jsonValueDecoder struct {
	ctx    context.Context
	dec    *json.Decoder
	stream *jsonStreamReader
}

func newJSONValueDecoder(ctx context.Context, r io.Reader) *jsonValueDecoder {
	stream := &jsonStreamReader{reader: r}
	dec := json.NewDecoder(stream)
	dec.UseNumber()
	return &jsonValueDecoder{ctx: ctx, dec: dec, stream: stream}
}

func (d *jsonValueDecoder) next() (value, error) {
	var raw json.RawMessage
	if err := d.dec.Decode(&raw); err != nil {
		return value{}, err
	}
	if len(raw) == 0 {
		return value{}, errors.New("empty JSON value")
	}
	if raw[0] != '{' && raw[0] != '[' && raw[0] != '"' {
		next, err := d.stream.peek(d.dec.InputOffset())
		if err != nil && !errors.Is(err, io.EOF) {
			return value{}, err
		}
		if err == nil && !isPrimitiveBoundary(next) {
			return value{}, fmt.Errorf("invalid character %q after top-level value", next)
		}
	}
	if err := d.ctx.Err(); err != nil {
		return value{}, err
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return (&jsonValueDecoder{ctx: d.ctx, dec: decoder}).decode(0)
}

type jsonStreamReader struct {
	reader     io.Reader
	delivered  []byte
	pending    []byte
	pendingErr error
}

func (r *jsonStreamReader) Read(p []byte) (int, error) {
	if len(r.pending) > 0 {
		n := copy(p, r.pending)
		r.delivered = append(r.delivered, r.pending[:n]...)
		r.pending = r.pending[n:]
		if len(r.pending) == 0 {
			err := r.pendingErr
			r.pendingErr = nil
			return n, err
		}
		return n, nil
	}
	n, err := r.reader.Read(p)
	if n > 0 {
		r.delivered = append(r.delivered, p[:n]...)
	}
	return n, err
}

func (r *jsonStreamReader) peek(offset int64) (byte, error) {
	if offset < 0 || offset > int64(len(r.delivered)) {
		return 0, errors.New("invalid JSON decoder offset")
	}
	if offset < int64(len(r.delivered)) {
		return r.delivered[offset], nil
	}
	if len(r.pending) > 0 {
		return r.pending[0], nil
	}
	for {
		var one [1]byte
		n, err := r.reader.Read(one[:])
		if n > 0 {
			r.pending = append(r.pending, one[:n]...)
			r.pendingErr = err
			return one[0], nil
		}
		if err != nil {
			return 0, err
		}
	}
}

func isPrimitiveBoundary(next byte) bool {
	switch next {
	case ' ', '\t', '\r', '\n', '{', '[', '"':
		return true
	default:
		return false
	}
}

func (d *jsonValueDecoder) decode(depth int) (value, error) {
	if err := d.ctx.Err(); err != nil {
		return value{}, err
	}
	token, err := d.dec.Token()
	if err != nil {
		return value{}, err
	}
	switch token := token.(type) {
	case nil:
		return null(), nil
	case bool:
		return boolean(token), nil
	case string:
		return stringValue(token)
	case json.Number:
		return parseNumber(string(token))
	case json.Delim:
		if depth >= MaxNestingDepth && (token == '[' || token == '{') {
			return value{}, errValueDepth
		}
		switch token {
		case '[':
			items := make([]value, 0)
			nodes, size := 1, 2
			for d.dec.More() {
				if err := d.ctx.Err(); err != nil {
					return value{}, err
				}
				item, err := d.decode(depth + 1)
				if err != nil {
					if errors.Is(err, io.EOF) {
						return value{}, io.ErrUnexpectedEOF
					}
					return value{}, err
				}
				separator := 0
				if len(items) > 0 {
					separator = 1
				}
				if err := addAggregate(&nodes, &size, item, separator, MaxValueNodes, MaxValueBytes); err != nil {
					return value{}, err
				}
				items = append(items, item)
			}
			end, err := d.dec.Token()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return value{}, io.ErrUnexpectedEOF
				}
				return value{}, err
			}
			if end != json.Delim(']') {
				return value{}, errors.New("invalid JSON array")
			}
			return arrayValue(items)
		case '{':
			items := make([]objectMember, 0)
			nodes, size := 1, 2
			for d.dec.More() {
				if err := d.ctx.Err(); err != nil {
					return value{}, err
				}
				keyToken, err := d.dec.Token()
				if err != nil {
					if errors.Is(err, io.EOF) {
						return value{}, io.ErrUnexpectedEOF
					}
					return value{}, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return value{}, errors.New("invalid JSON object key")
				}
				item, err := d.decode(depth + 1)
				if err != nil {
					if errors.Is(err, io.EOF) {
						return value{}, io.ErrUnexpectedEOF
					}
					return value{}, err
				}
				separator := 0
				if len(items) > 0 {
					separator = 1
				}
				if err := addAggregate(&nodes, &size, item, len(key)+3+separator, MaxValueNodes, MaxValueBytes); err != nil {
					return value{}, err
				}
				items = append(items, objectMember{key: key, value: item})
			}
			end, err := d.dec.Token()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return value{}, io.ErrUnexpectedEOF
				}
				return value{}, err
			}
			if end != json.Delim('}') {
				return value{}, errors.New("invalid JSON object")
			}
			return object(items)
		default:
			return value{}, errors.New("unexpected closing delimiter")
		}
	default:
		return value{}, errors.New("unsupported JSON token")
	}
}

func parseSingleJSON(ctx context.Context, text string) (value, error) {
	if len(text) > MaxValueBytes {
		return value{}, errValueBytes
	}
	if err := validateSurrogates(text); err != nil {
		return value{}, err
	}
	decoder := newJSONValueDecoder(ctx, strings.NewReader(text))
	v, err := decoder.next()
	if err != nil {
		return value{}, err
	}
	_, err = decoder.next()
	if !errors.Is(err, io.EOF) {
		if err == nil {
			return value{}, errors.New("expected exactly one JSON value")
		}
		return value{}, err
	}
	return v, nil
}

type boundedBuilder struct {
	b   strings.Builder
	max int
}

func (b *boundedBuilder) writeString(s string) error {
	if len(s) > b.max-b.b.Len() {
		return errOutputLimit
	}
	_, _ = b.b.WriteString(s)
	return nil
}

func (b *boundedBuilder) writeByte(c byte) error {
	if b.b.Len() >= b.max {
		return errOutputLimit
	}
	_ = b.b.WriteByte(c)
	return nil
}

func encodeValue(v value, pretty bool, limit int) (string, error) {
	b := &boundedBuilder{max: limit}
	if err := appendValue(b, v, pretty, 0); err != nil {
		return "", err
	}
	return b.b.String(), nil
}

func appendValue(b *boundedBuilder, v value, pretty bool, depth int) error {
	switch v.kind {
	case valueNull:
		return b.writeString("null")
	case valueBoolean:
		if v.bool {
			return b.writeString("true")
		}
		return b.writeString("false")
	case valueNumber:
		return b.writeString(v.num.text())
	case valueString:
		return appendJSONString(b, v.str)
	case valueArray:
		if err := b.writeByte('['); err != nil {
			return err
		}
		for i := range v.array {
			if i > 0 {
				if err := b.writeByte(','); err != nil {
					return err
				}
			}
			if pretty {
				if err := b.writeByte('\n'); err != nil {
					return err
				}
				if err := appendIndent(b, depth+1); err != nil {
					return err
				}
			}
			if err := appendValue(b, v.array[i], pretty, depth+1); err != nil {
				return err
			}
		}
		if pretty && len(v.array) > 0 {
			if err := b.writeByte('\n'); err != nil {
				return err
			}
			if err := appendIndent(b, depth); err != nil {
				return err
			}
		}
		return b.writeByte(']')
	case valueObject:
		if err := b.writeByte('{'); err != nil {
			return err
		}
		for i := range v.obj.members {
			if i > 0 {
				if err := b.writeByte(','); err != nil {
					return err
				}
			}
			if pretty {
				if err := b.writeByte('\n'); err != nil {
					return err
				}
				if err := appendIndent(b, depth+1); err != nil {
					return err
				}
			}
			member := &v.obj.members[i]
			if err := appendJSONString(b, member.key); err != nil {
				return err
			}
			if pretty {
				if err := b.writeString(": "); err != nil {
					return err
				}
			} else if err := b.writeByte(':'); err != nil {
				return err
			}
			if err := appendValue(b, member.value, pretty, depth+1); err != nil {
				return err
			}
		}
		if pretty && len(v.obj.members) > 0 {
			if err := b.writeByte('\n'); err != nil {
				return err
			}
			if err := appendIndent(b, depth); err != nil {
				return err
			}
		}
		return b.writeByte('}')
	default:
		return errors.New("invalid value")
	}
}

func appendIndent(b *boundedBuilder, depth int) error {
	for range depth * 2 {
		if err := b.writeByte(' '); err != nil {
			return err
		}
	}
	return nil
}

func appendJSONString(b *boundedBuilder, s string) error {
	if err := b.writeByte('"'); err != nil {
		return err
	}
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		switch r {
		case '"':
			if err := b.writeString(`\"`); err != nil {
				return err
			}
		case '\\':
			if err := b.writeString(`\\`); err != nil {
				return err
			}
		case '\b':
			if err := b.writeString(`\b`); err != nil {
				return err
			}
		case '\f':
			if err := b.writeString(`\f`); err != nil {
				return err
			}
		case '\n':
			if err := b.writeString(`\n`); err != nil {
				return err
			}
		case '\r':
			if err := b.writeString(`\r`); err != nil {
				return err
			}
		case '\t':
			if err := b.writeString(`\t`); err != nil {
				return err
			}
		default:
			if r < 0x20 || r == 0x7f {
				if err := b.writeString(fmt.Sprintf(`\u%04x`, r)); err != nil {
					return err
				}
			} else if err := b.writeString(string(r)); err != nil {
				return err
			}
		}
	}
	return b.writeByte('"')
}
