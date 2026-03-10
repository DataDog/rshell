// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package internal implements the core awk language: lexer, parser, and interpreter.
package awkcore

// Token types for the awk lexer.
type tokenType int

const (
	tokEOF tokenType = iota
	tokNEWLINE
	tokSEMI
	tokLBRACE
	tokRBRACE
	tokLPAREN
	tokRPAREN
	tokLBRACKET
	tokRBRACKET
	tokCOMMA
	tokDOLLAR
	tokNOT

	// Arithmetic
	tokPLUS
	tokMINUS
	tokSTAR
	tokSLASH
	tokPERCENT
	tokPOWER

	// Comparison
	tokLT
	tokLE
	tokGT
	tokGE
	tokEQ
	tokNE

	// Assignment
	tokASSIGN
	tokPLUSASSIGN
	tokMINUSASSIGN
	tokSTARASSIGN
	tokSLASHASSIGN
	tokPERCENTASSIGN
	tokPOWERASSIGN

	// Logical
	tokAND
	tokOR

	// Match
	tokMATCH    // ~
	tokNOTMATCH // !~

	// Increment/decrement
	tokINCR // ++
	tokDECR // --

	// String concatenation is implicit (space)
	tokAPPEND // >>

	// Pipe
	tokPIPE // |

	// Ternary
	tokQUESTION
	tokCOLON

	// Literals
	tokNUMBER
	tokSTRING
	tokREGEX

	// Identifiers and keywords
	tokIDENT
	tokBEGIN
	tokEND
	tokIF
	tokELSE
	tokWHILE
	tokFOR
	tokDO
	tokBREAK
	tokCONTINUE
	tokNEXT
	tokEXIT
	tokDELETE
	tokIN
	tokGETLINE
	tokPRINT
	tokPRINTF
	tokFUNCTION
	tokRETURN
)

type token struct {
	typ tokenType
	val string
	pos int
}

// keywords maps awk keywords to their token types.
var keywords = map[string]tokenType{
	"BEGIN":    tokBEGIN,
	"END":      tokEND,
	"if":       tokIF,
	"else":     tokELSE,
	"while":    tokWHILE,
	"for":      tokFOR,
	"do":       tokDO,
	"break":    tokBREAK,
	"continue": tokCONTINUE,
	"next":     tokNEXT,
	"exit":     tokEXIT,
	"delete":   tokDELETE,
	"in":       tokIN,
	"getline":  tokGETLINE,
	"print":    tokPRINT,
	"printf":   tokPRINTF,
	"function": tokFUNCTION,
	"return":   tokRETURN,
}
