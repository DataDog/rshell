// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tcpdump

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// Filter is a compiled BPF-style filter expression that can match gopacket
// packets. Instances are created by compileFilter and are immutable once
// created.
type Filter struct {
	root filterNode
}

// Matches returns true if the packet satisfies the filter expression.
func (f *Filter) Matches(pkt gopacket.Packet) bool {
	return f.root.eval(pkt)
}

// filterNode is a node in the filter expression tree.
type filterNode interface {
	eval(pkt gopacket.Packet) bool
}

// andNode evaluates to true only when both sub-nodes are true.
type andNode struct{ left, right filterNode }

func (n *andNode) eval(pkt gopacket.Packet) bool { return n.left.eval(pkt) && n.right.eval(pkt) }

// orNode evaluates to true when either sub-node is true.
type orNode struct{ left, right filterNode }

func (n *orNode) eval(pkt gopacket.Packet) bool { return n.left.eval(pkt) || n.right.eval(pkt) }

// notNode negates its child node.
type notNode struct{ child filterNode }

func (n *notNode) eval(pkt gopacket.Packet) bool { return !n.child.eval(pkt) }

// hostNode matches if src or dst IP equals addr (or, with dir, only that direction).
type hostNode struct {
	addr net.IP
	dir  string // "src", "dst", or "" (either)
}

func (n *hostNode) eval(pkt gopacket.Packet) bool {
	src, dst := packetIPs(pkt)
	switch n.dir {
	case "src":
		return src != nil && src.Equal(n.addr)
	case "dst":
		return dst != nil && dst.Equal(n.addr)
	default:
		return (src != nil && src.Equal(n.addr)) || (dst != nil && dst.Equal(n.addr))
	}
}

// portNode matches if src or dst TCP/UDP port equals the given port.
type portNode struct {
	port uint16
	dir  string // "src", "dst", or "" (either)
}

func (n *portNode) eval(pkt gopacket.Packet) bool {
	src, dst := packetPorts(pkt)
	switch n.dir {
	case "src":
		return src == n.port
	case "dst":
		return dst == n.port
	default:
		return src == n.port || dst == n.port
	}
}

// protoNode matches if the packet is of the given protocol.
type protoNode struct{ proto string }

func (n *protoNode) eval(pkt gopacket.Packet) bool {
	switch n.proto {
	case "tcp":
		return pkt.Layer(layers.LayerTypeTCP) != nil
	case "udp":
		return pkt.Layer(layers.LayerTypeUDP) != nil
	case "icmp":
		return pkt.Layer(layers.LayerTypeICMPv4) != nil
	case "icmp6":
		return pkt.Layer(layers.LayerTypeICMPv6) != nil
	case "ip":
		return pkt.Layer(layers.LayerTypeIPv4) != nil
	case "ip6":
		return pkt.Layer(layers.LayerTypeIPv6) != nil
	}
	return false
}

// trueNode always matches (used for empty filters or as a fallback).
type trueNode struct{}

func (n *trueNode) eval(_ gopacket.Packet) bool { return true }

// packetIPs extracts the source and destination IP addresses from a packet.
func packetIPs(pkt gopacket.Packet) (src, dst net.IP) {
	if ipv4Layer := pkt.Layer(layers.LayerTypeIPv4); ipv4Layer != nil {
		ip := ipv4Layer.(*layers.IPv4)
		return ip.SrcIP, ip.DstIP
	}
	if ipv6Layer := pkt.Layer(layers.LayerTypeIPv6); ipv6Layer != nil {
		ip := ipv6Layer.(*layers.IPv6)
		return ip.SrcIP, ip.DstIP
	}
	return nil, nil
}

// packetPorts extracts the source and destination ports from a TCP or UDP packet.
func packetPorts(pkt gopacket.Packet) (src, dst uint16) {
	if tcpLayer := pkt.Layer(layers.LayerTypeTCP); tcpLayer != nil {
		tcp := tcpLayer.(*layers.TCP)
		return uint16(tcp.SrcPort), uint16(tcp.DstPort)
	}
	if udpLayer := pkt.Layer(layers.LayerTypeUDP); udpLayer != nil {
		udp := udpLayer.(*layers.UDP)
		return uint16(udp.SrcPort), uint16(udp.DstPort)
	}
	return 0, 0
}

// ---------------------------------------------------------------------------
// Filter expression compiler (recursive descent parser)
// ---------------------------------------------------------------------------

// tokenizer splits a filter string into tokens.
type tokenizer struct {
	tokens []string
	pos    int
}

func newTokenizer(expr string) *tokenizer {
	return &tokenizer{tokens: tokenize(expr)}
}

// tokenize splits the expression on whitespace and parentheses.
func tokenize(expr string) []string {
	expr = strings.TrimSpace(expr)
	var tokens []string
	var cur strings.Builder
	for _, ch := range expr {
		if ch == '(' || ch == ')' {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
			tokens = append(tokens, string(ch))
		} else if ch == ' ' || ch == '\t' {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		} else {
			cur.WriteRune(ch)
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

func (t *tokenizer) peek() string {
	if t.pos >= len(t.tokens) {
		return ""
	}
	return t.tokens[t.pos]
}

func (t *tokenizer) next() string {
	tok := t.peek()
	if tok != "" {
		t.pos++
	}
	return tok
}

func (t *tokenizer) expect(want string) error {
	got := t.next()
	if got != want {
		return fmt.Errorf("expected %q, got %q", want, got)
	}
	return nil
}

// compileFilter parses a BPF-style filter expression into a Filter.
// Supported: host, src/dst host, port, src/dst port, tcp, udp, icmp, ip, ip6,
// and/&&, or/||, not/!, parentheses.
func compileFilter(expr string) (*Filter, error) {
	if strings.TrimSpace(expr) == "" {
		return &Filter{root: &trueNode{}}, nil
	}
	tok := newTokenizer(expr)
	node, err := parseOr(tok)
	if err != nil {
		return nil, err
	}
	if tok.peek() != "" {
		return nil, fmt.Errorf("unexpected token %q", tok.peek())
	}
	return &Filter{root: node}, nil
}

// parseOr handles the lowest-precedence binary operator: or / ||.
func parseOr(tok *tokenizer) (filterNode, error) {
	left, err := parseAnd(tok)
	if err != nil {
		return nil, err
	}
	for {
		p := strings.ToLower(tok.peek())
		if p != "or" && p != "||" {
			break
		}
		tok.next()
		right, err := parseAnd(tok)
		if err != nil {
			return nil, err
		}
		left = &orNode{left: left, right: right}
	}
	return left, nil
}

// parseAnd handles the and / && operator.
func parseAnd(tok *tokenizer) (filterNode, error) {
	left, err := parseUnary(tok)
	if err != nil {
		return nil, err
	}
	for {
		p := strings.ToLower(tok.peek())
		if p != "and" && p != "&&" {
			break
		}
		tok.next()
		right, err := parseUnary(tok)
		if err != nil {
			return nil, err
		}
		left = &andNode{left: left, right: right}
	}
	return left, nil
}

// parseUnary handles not / ! and parentheses.
func parseUnary(tok *tokenizer) (filterNode, error) {
	p := strings.ToLower(tok.peek())
	if p == "not" || p == "!" {
		tok.next()
		child, err := parseUnary(tok)
		if err != nil {
			return nil, err
		}
		return &notNode{child: child}, nil
	}
	if p == "(" {
		tok.next()
		node, err := parseOr(tok)
		if err != nil {
			return nil, err
		}
		if err := tok.expect(")"); err != nil {
			return nil, err
		}
		return node, nil
	}
	return parsePrimitive(tok)
}

// parsePrimitive handles the leaf filter terms.
func parsePrimitive(tok *tokenizer) (filterNode, error) {
	word := strings.ToLower(tok.next())
	if word == "" {
		return nil, errors.New("unexpected end of filter expression")
	}

	switch word {
	case "tcp", "udp", "icmp", "icmp6", "ip", "ip6":
		return &protoNode{proto: word}, nil

	case "host":
		addr := tok.next()
		if addr == "" {
			return nil, errors.New("missing address after 'host'")
		}
		ip := net.ParseIP(addr)
		if ip == nil {
			return nil, fmt.Errorf("invalid host address: %q", addr)
		}
		return &hostNode{addr: ip, dir: ""}, nil

	case "port":
		portStr := tok.next()
		if portStr == "" {
			return nil, errors.New("missing port after 'port'")
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 0 || port > 65535 {
			return nil, fmt.Errorf("invalid port: %q", portStr)
		}
		return &portNode{port: uint16(port), dir: ""}, nil

	case "src", "dst":
		dir := word
		qualifier := strings.ToLower(tok.next())
		switch qualifier {
		case "host":
			addr := tok.next()
			if addr == "" {
				return nil, fmt.Errorf("missing address after '%s host'", dir)
			}
			ip := net.ParseIP(addr)
			if ip == nil {
				return nil, fmt.Errorf("invalid host address: %q", addr)
			}
			return &hostNode{addr: ip, dir: dir}, nil
		case "port":
			portStr := tok.next()
			if portStr == "" {
				return nil, fmt.Errorf("missing port after '%s port'", dir)
			}
			port, err := strconv.Atoi(portStr)
			if err != nil || port < 0 || port > 65535 {
				return nil, fmt.Errorf("invalid port: %q", portStr)
			}
			return &portNode{port: uint16(port), dir: dir}, nil
		default:
			return nil, fmt.Errorf("unexpected qualifier %q after %q", qualifier, dir)
		}

	default:
		return nil, fmt.Errorf("unknown filter primitive: %q", word)
	}
}
