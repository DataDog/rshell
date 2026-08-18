// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"context"
	"regexp/syntax"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	awkRegexBoundaryPrefixMarker = 0xd900
	awkRegexWordBoundaryMarker   = 0xd910
	awkRegexNoWordBoundaryMarker = 0xd911
	awkRegexBeginWordMarker      = 0xd912
	awkRegexEndWordMarker        = 0xd913

	awkRegexEmptyWordBoundary uint32 = 1 << (8 + iota)
	awkRegexEmptyNoWordBoundary
	awkRegexEmptyBeginWord
	awkRegexEmptyEndWord
	awkRegexEmptyWordMask = awkRegexEmptyWordBoundary |
		awkRegexEmptyNoWordBoundary |
		awkRegexEmptyBeginWord |
		awkRegexEmptyEndWord

	awkBoundaryCaptureGCMinimum    = 4 << 10
	awkBoundaryMaxRetainedCaptures = 8 << 10
)

func compileAwkBoundaryProg(pattern string) (*syntax.Prog, error) {
	if !strings.Contains(pattern, `\x{d90`) {
		return nil, nil
	}
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, err
	}
	prog, err := syntax.Compile(parsed.Simplify())
	if err != nil {
		return nil, err
	}
	found := false
	for i := range prog.Inst {
		if awkRegexBoundaryPrefixInst(&prog.Inst[i]) {
			prog.Inst[i].Op = syntax.InstNop
			prog.Inst[i].Arg = 0
			prog.Inst[i].Rune = nil
			continue
		}
		mask := awkRegexBoundaryInstMask(&prog.Inst[i])
		if mask == 0 {
			continue
		}
		prog.Inst[i].Op = syntax.InstEmptyWidth
		prog.Inst[i].Arg = mask
		prog.Inst[i].Rune = nil
		found = true
	}
	if !found {
		return nil, nil
	}
	return prog, nil
}

func awkRegexBoundaryPrefixInst(inst *syntax.Inst) bool {
	return (inst.Op == syntax.InstRune || inst.Op == syntax.InstRune1) &&
		len(inst.Rune) == 1 && inst.Rune[0] == awkRegexBoundaryPrefixMarker
}

func awkRegexBoundaryInstMask(inst *syntax.Inst) uint32 {
	if inst.Op != syntax.InstRune && inst.Op != syntax.InstRune1 {
		return 0
	}
	if len(inst.Rune) == 1 {
		return awkRegexBoundaryRuneMask(inst.Rune[0])
	}
	if len(inst.Rune)%2 != 0 {
		return 0
	}
	for i := 0; i < len(inst.Rune); i += 2 {
		if inst.Rune[i] < awkRegexWordBoundaryMarker || inst.Rune[i+1] > awkRegexEndWordMarker {
			return 0
		}
	}
	var mask uint32
	for r := rune(awkRegexWordBoundaryMarker); r <= awkRegexEndWordMarker; r++ {
		if inst.MatchRune(r) {
			mask |= awkRegexBoundaryRuneMask(r)
		}
	}
	return mask
}

func awkRegexBoundaryRuneMask(r rune) uint32 {
	switch r {
	case awkRegexWordBoundaryMarker:
		return awkRegexEmptyWordBoundary
	case awkRegexNoWordBoundaryMarker:
		return awkRegexEmptyNoWordBoundary
	case awkRegexBeginWordMarker:
		return awkRegexEmptyBeginWord
	case awkRegexEndWordMarker:
		return awkRegexEmptyEndWord
	default:
		return 0
	}
}

type awkBoundaryThread struct {
	pc      uint32
	start   int
	history int
}

type awkBoundaryCapture struct {
	slot, pos, previous int
}

type awkBoundaryQueue struct {
	seen    []bool
	visited []uint32
	threads []awkBoundaryThread
}

type awkBoundaryMachine struct {
	prog              *syntax.Prog
	runq              awkBoundaryQueue
	nextq             awkBoundaryQueue
	captures          []awkBoundaryCapture
	captureMarks      []bool
	captureRemap      []int
	captureEpoch      []uint32
	captureGeneration uint32
	captureGCAt       int
	matched           bool
	matchStart        int
	matchEnd          int
	matchHistory      int
	recordCaptures    bool
	searchStart       int
	resetWordBoundary bool
	ctx               context.Context
	work              uint32
	canceled          bool
}

func findAwkBoundaryRegexFrom(re *awkRegex, s string, search int, submatches, bytewise bool) []int {
	re.boundaryMachineMu.Lock()
	defer re.boundaryMachineMu.Unlock()
	if re.boundaryMachine == nil {
		re.boundaryMachine = newAwkBoundaryMachine(re.boundaryProg)
	}
	return re.boundaryMachine.find(re.ctx, s, search, submatches, re.byteMode || bytewise && search > 0, false)
}

func findAwkSplitRegexFrom(re *awkRegex, s string, search int) []int {
	if re.boundaryProg == nil {
		return findAwkRegexFrom(re, s, search, false)
	}
	re.boundaryMachineMu.Lock()
	defer re.boundaryMachineMu.Unlock()
	if re.boundaryMachine == nil {
		re.boundaryMachine = newAwkBoundaryMachine(re.boundaryProg)
	}
	return re.boundaryMachine.find(re.ctx, s, search, false, re.byteMode, search > 0)
}

func newAwkBoundaryMachine(prog *syntax.Prog) *awkBoundaryMachine {
	return &awkBoundaryMachine{
		prog:  prog,
		runq:  awkBoundaryQueue{seen: make([]bool, len(prog.Inst))},
		nextq: awkBoundaryQueue{seen: make([]bool, len(prog.Inst))},
	}
}

func (m *awkBoundaryMachine) find(ctx context.Context, s string, search int, submatches, mapInvalidBytes, resetWordBoundary bool) []int {
	defer m.releaseCaptureStorage()
	m.clearQueue(&m.runq)
	m.clearQueue(&m.nextq)
	m.captures = m.captures[:0]
	m.captureGCAt = awkBoundaryCaptureGCMinimum
	m.matched = false
	m.matchStart, m.matchEnd, m.matchHistory = -1, -1, -1
	m.recordCaptures = submatches
	m.searchStart = search
	m.resetWordBoundary = resetWordBoundary
	m.ctx, m.work, m.canceled = ctx, 0, false

	before := awkBoundaryRuneBefore(s, search, mapInvalidBytes)
	current, width := awkBoundaryRuneAt(s, search, mapInvalidBytes)
	pos := search
	runq, nextq := &m.runq, &m.nextq
	for {
		if ctx != nil && ctx.Err() != nil {
			m.canceled = true
			break
		}
		if len(runq.threads) == 0 && m.matched {
			break
		}
		if !m.matched {
			m.add(runq, uint32(m.prog.Start), pos, pos, -1, before, current)
		}

		nextPos := pos + width
		after, nextWidth := awkBoundaryRuneAt(s, nextPos, mapInvalidBytes)
		m.step(runq, nextq, pos, nextPos, current, after)
		if !m.canceled {
			m.compactCaptureHistory(nextq)
		}
		if width == 0 {
			break
		}
		pos = nextPos
		before, current, width = current, after, nextWidth
		runq, nextq = nextq, runq
	}
	m.clearQueue(runq)
	m.clearQueue(nextq)
	if !m.matched || m.canceled {
		return nil
	}
	ncap := 2
	if submatches {
		ncap = max(ncap, m.prog.NumCap)
	}
	match := make([]int, ncap)
	for i := range match {
		match[i] = -1
	}
	match[0], match[1] = m.matchStart, m.matchEnd
	for history := m.matchHistory; history >= 0; history = m.captures[history].previous {
		capture := m.captures[history]
		if capture.slot < len(match) && match[capture.slot] < 0 {
			match[capture.slot] = capture.pos
		}
	}
	return match
}

func (m *awkBoundaryMachine) step(runq, nextq *awkBoundaryQueue, pos, nextPos int, current, after rune) {
	for _, thread := range runq.threads {
		if m.checkCanceled() {
			break
		}
		inst := &m.prog.Inst[thread.pc]
		if m.matched && thread.start > m.matchStart {
			continue
		}
		switch inst.Op {
		case syntax.InstMatch:
			if !m.matched || m.matchEnd < pos {
				m.matchStart, m.matchEnd, m.matchHistory = thread.start, pos, thread.history
			}
			m.matched = true
		case syntax.InstRune, syntax.InstRune1:
			if current >= 0 && inst.MatchRune(current) {
				m.add(nextq, inst.Out, nextPos, thread.start, thread.history, current, after)
			}
		case syntax.InstRuneAny:
			if current >= 0 {
				m.add(nextq, inst.Out, nextPos, thread.start, thread.history, current, after)
			}
		case syntax.InstRuneAnyNotNL:
			if current >= 0 && current != '\n' {
				m.add(nextq, inst.Out, nextPos, thread.start, thread.history, current, after)
			}
		}
	}
	m.clearQueue(runq)
}

func (m *awkBoundaryMachine) add(q *awkBoundaryQueue, pc uint32, pos, start, history int, before, after rune) {
	if pc == 0 || q.seen[pc] || m.checkCanceled() {
		return
	}
	q.seen[pc] = true
	q.visited = append(q.visited, pc)
	inst := &m.prog.Inst[pc]
	switch inst.Op {
	case syntax.InstAlt, syntax.InstAltMatch:
		m.add(q, inst.Out, pos, start, history, before, after)
		m.add(q, inst.Arg, pos, start, history, before, after)
	case syntax.InstCapture:
		if m.recordCaptures {
			m.captures = append(m.captures, awkBoundaryCapture{slot: int(inst.Arg), pos: pos, previous: history})
			history = len(m.captures) - 1
		}
		m.add(q, inst.Out, pos, start, history, before, after)
	case syntax.InstEmptyWidth:
		wordBefore := before
		if m.resetWordBoundary && pos == m.searchStart {
			wordBefore = -1
		}
		if awkRegexEmptyWidthMatches(inst.Arg, before, wordBefore, after) {
			m.add(q, inst.Out, pos, start, history, before, after)
		}
	case syntax.InstNop:
		m.add(q, inst.Out, pos, start, history, before, after)
	case syntax.InstMatch, syntax.InstRune, syntax.InstRune1, syntax.InstRuneAny, syntax.InstRuneAnyNotNL:
		q.threads = append(q.threads, awkBoundaryThread{pc: pc, start: start, history: history})
	}
}

func (m *awkBoundaryMachine) checkCanceled() bool {
	if m.canceled {
		return true
	}
	m.work++
	if m.work&255 == 0 && m.ctx != nil && m.ctx.Err() != nil {
		m.canceled = true
	}
	return m.canceled
}

func (m *awkBoundaryMachine) clearQueue(q *awkBoundaryQueue) {
	q.threads = q.threads[:0]
	for _, pc := range q.visited {
		q.seen[pc] = false
	}
	q.visited = q.visited[:0]
}

func (m *awkBoundaryMachine) compactCaptureHistory(q *awkBoundaryQueue) {
	if !m.recordCaptures || len(m.captures) < m.captureGCAt {
		return
	}
	if m.checkCanceled() {
		return
	}
	count := len(m.captures)
	if cap(m.captureMarks) < count {
		m.captureMarks = make([]bool, count)
	} else {
		m.captureMarks = m.captureMarks[:count]
		for i := range m.captureMarks {
			m.captureMarks[i] = false
			if m.checkCanceled() {
				return
			}
		}
	}
	if cap(m.captureRemap) < count {
		m.captureRemap = make([]int, count)
	} else {
		m.captureRemap = m.captureRemap[:count]
	}
	if cap(m.captureEpoch) < m.prog.NumCap {
		m.captureEpoch = make([]uint32, m.prog.NumCap)
	} else {
		m.captureEpoch = m.captureEpoch[:m.prog.NumCap]
	}
	mark := func(history int) bool {
		if m.checkCanceled() {
			return false
		}
		m.captureGeneration++
		if m.captureGeneration == 0 {
			clear(m.captureEpoch)
			m.captureGeneration++
		}
		generation := m.captureGeneration
		for history >= 0 {
			if m.checkCanceled() {
				return false
			}
			capture := m.captures[history]
			if m.captureEpoch[capture.slot] != generation {
				m.captureEpoch[capture.slot] = generation
				m.captureMarks[history] = true
			}
			history = m.captures[history].previous
		}
		return true
	}
	if !mark(m.matchHistory) {
		return
	}
	for i := range q.threads {
		if !mark(q.threads[i].history) {
			return
		}
	}

	write := 0
	for read, capture := range m.captures {
		if m.checkCanceled() {
			return
		}
		if capture.previous >= 0 {
			capture.previous = m.captureRemap[capture.previous]
		}
		m.captureRemap[read] = capture.previous
		if m.captureMarks[read] {
			m.captures[write] = capture
			m.captureRemap[read] = write
			write++
		}
	}
	if m.matchHistory >= 0 {
		m.matchHistory = m.captureRemap[m.matchHistory]
	}
	for i := range q.threads {
		if m.checkCanceled() {
			return
		}
		if q.threads[i].history >= 0 {
			q.threads[i].history = m.captureRemap[q.threads[i].history]
		}
	}
	m.captures = m.captures[:write]
	m.captureGCAt = max(awkBoundaryCaptureGCMinimum, write*2)
}

func (m *awkBoundaryMachine) releaseCaptureStorage() {
	if cap(m.captures) > awkBoundaryMaxRetainedCaptures {
		m.captures = nil
	}
	if cap(m.captureMarks) > awkBoundaryMaxRetainedCaptures {
		m.captureMarks = nil
	}
	if cap(m.captureRemap) > awkBoundaryMaxRetainedCaptures {
		m.captureRemap = nil
	}
	if cap(m.captureEpoch) > awkBoundaryMaxRetainedCaptures {
		m.captureEpoch = nil
	}
}

func awkRegexEmptyWidthMatches(op uint32, before, wordBefore, after rune) bool {
	if required := syntax.EmptyOp(op); required&^syntax.EmptyOpContext(before, after) != 0 {
		return false
	}
	custom := op & awkRegexEmptyWordMask
	if custom == 0 {
		return true
	}
	beforeWord, afterWord := isAwkRegexWordRune(wordBefore), isAwkRegexWordRune(after)
	return custom&awkRegexEmptyWordBoundary != 0 && beforeWord != afterWord ||
		custom&awkRegexEmptyNoWordBoundary != 0 && beforeWord == afterWord ||
		custom&awkRegexEmptyBeginWord != 0 && !beforeWord && afterWord ||
		custom&awkRegexEmptyEndWord != 0 && beforeWord && !afterWord
}

func isAwkRegexWordRune(r rune) bool {
	return r == '_' || unicode.Is(unicode.L, r) || unicode.Is(unicode.Nl, r) ||
		unicode.Is(unicode.Nd, r) || unicode.Is(unicode.Other_Alphabetic, r) ||
		r >= 0x2ebf0 && r <= 0x2ee5d
}

func awkBoundaryRuneAt(s string, pos int, mapInvalidBytes bool) (rune, int) {
	if pos >= len(s) {
		return -1, 0
	}
	r, size := utf8.DecodeRuneInString(s[pos:])
	if mapInvalidBytes && r == utf8.RuneError && size == 1 {
		return awkRegexByteRuneBase + rune(s[pos]), 1
	}
	return r, size
}

func awkBoundaryRuneBefore(s string, pos int, mapInvalidBytes bool) rune {
	if pos <= 0 {
		return -1
	}
	size := previousAwkRuneSize(s, pos)
	r, decodedSize := utf8.DecodeRuneInString(s[pos-size : pos])
	if mapInvalidBytes && r == utf8.RuneError && decodedSize == 1 {
		return awkRegexByteRuneBase + rune(s[pos-1])
	}
	return r
}
