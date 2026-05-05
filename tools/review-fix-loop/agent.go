// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/charmbracelet/glamour"
)

const maxToolRounds = 300

// Agent runs a Claude agent loop: sends messages, handles tool calls, streams text output.
type Agent struct {
	client    anthropic.Client
	model     string
	maxTokens int64
	workDir   string
	verbose   bool
	out       io.Writer // dots + banners → stdout + log file
	logOut    io.Writer // all streaming text (verbose detail) → log file only
	termOut   io.Writer // per-skill summary → stdout only
}

func newAgent(cfg Config, out, logOut, termOut io.Writer) *Agent {
	return &Agent{
		client:    anthropic.NewClient(),
		model:     cfg.Model,
		maxTokens: cfg.MaxTokens,
		workDir:   cfg.WorkDir,
		verbose:   cfg.Verbose,
		out:       out,
		logOut:    logOut,
		termOut:   termOut,
	}
}

// Run executes the skill identified by name with the given system prompt and user message.
// In normal mode: dots go to stdout for each tool call; only the final agent response
// (the summary) is printed to stdout, prefixed with [name]. Everything goes to the log.
// In verbose mode: banners, raw Claude text, and full bash I/O are shown on stdout.
func (a *Agent) Run(ctx context.Context, name, systemPrompt, userMessage string) (string, error) {
	colorCode := agentColor(name)
	prefix := paint("["+name+"] ", colorCode)

	if a.verbose {
		fmt.Fprintf(a.out, "\n╔═ %s ═══════════════════════\n", paint("["+name+"]", colorCode))
	} else {
		fmt.Fprintf(a.logOut, "\n╔═ [%s] ═══════════════════════\n", name)
		fmt.Fprintf(a.termOut, "\n%s\n", paint("["+name+"]", colorCode))
	}

	// lw writes prefixed text to the appropriate output depending on mode.
	// In verbose mode it goes to a.out (terminal+log); in normal mode to a.logOut (log only).
	var lw *lineWriter
	if a.verbose {
		lw = newLineWriter(a.out, prefix)
	} else {
		lw = newLineWriter(a.logOut, "["+name+"] ")
	}

	messages := []anthropic.MessageParam{
		{
			Role: anthropic.MessageParamRoleUser,
			Content: []anthropic.ContentBlockParamUnion{
				{OfText: &anthropic.TextBlockParam{Text: userMessage}},
			},
		},
	}

	tools := []anthropic.ToolUnionParam{bashToolParam()}

	// lastRoundText buffers the final round's text so it can be printed to the terminal.
	var lastRoundText strings.Builder
	// dotsDirty tracks whether dots were printed to stdout without a trailing newline.
	var dotsDirty bool
	reachedFinalResponse := false
	// Always terminate trailing dots so that log output from the caller never shares a line with them.
	defer func() {
		if !a.verbose && dotsDirty {
			fmt.Fprintln(a.termOut)
		}
	}()

	for round := 0; round < maxToolRounds; round++ {
		stream := a.client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
			Model:     a.model,
			MaxTokens: a.maxTokens,
			System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
			Messages:  messages,
			Tools:     tools,
		})

		var acc anthropic.Message
		lastRoundText.Reset()
		for stream.Next() {
			event := stream.Current()
			if err := acc.Accumulate(event); err != nil {
				return "", fmt.Errorf("accumulate stream: %w", err)
			}
			if e, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
				if d, ok := e.Delta.AsAny().(anthropic.TextDelta); ok {
					lw.write(d.Text)
					if !a.verbose {
						lastRoundText.WriteString(d.Text)
					}
				}
			}
		}
		if err := stream.Err(); err != nil {
			return "", fmt.Errorf("[%s] stream error (round %d): %w", name, round, err)
		}
		lw.flush()

		if acc.StopReason != anthropic.StopReasonToolUse {
			if !a.verbose {
				// Render the final round's text as formatted markdown on the terminal.
				if dotsDirty {
					fmt.Fprintln(a.termOut)
					dotsDirty = false
				}
				renderMarkdown(a.termOut, lastRoundText.String())
			}
			reachedFinalResponse = true
			break
		}

		// Append the assistant turn to history
		messages = append(messages, acc.ToParam())

		// Execute all tool calls and collect results
		var resultContent []anthropic.ContentBlockParamUnion
		for _, block := range acc.Content {
			if block.Type != "tool_use" {
				continue
			}
			var cmdInput struct {
				Command string `json:"command"`
			}
			json.Unmarshal(block.Input, &cmdInput) //nolint:errcheck
			if a.verbose {
				fmt.Fprintf(a.out, "  $ %s\n", cmdInput.Command)
			} else {
				fmt.Fprintf(a.logOut, "  $ %s\n", cmdInput.Command)
				fmt.Fprint(a.out, dim("."))
				dotsDirty = true
			}
			output, isErr := a.executeBash(ctx, block.Input)
			if !a.verbose {
				fmt.Fprintf(a.logOut, "%s\n", output)
			}
			tr := anthropic.ToolResultBlockParam{
				ToolUseID: block.ID,
				Content: []anthropic.ToolResultBlockParamContentUnion{
					{OfText: &anthropic.TextBlockParam{Text: output}},
				},
			}
			if isErr {
				tr.IsError = param.NewOpt(true)
			}
			resultContent = append(resultContent, anthropic.ContentBlockParamUnion{OfToolResult: &tr})
		}
		messages = append(messages, anthropic.MessageParam{
			Role:    anthropic.MessageParamRoleUser,
			Content: resultContent,
		})
	}

	// Terminate any trailing dots in verbose mode (non-verbose is handled by the defer above).
	if a.verbose && lw.dirtyLine {
		fmt.Fprintln(a.out)
		lw.dirtyLine = false
	}

	if a.verbose {
		fmt.Fprintf(a.out, "╚═ %s done ═══════════════════\n", paint("["+name+"]", colorCode))
	} else {
		fmt.Fprintf(a.logOut, "╚═ [%s] done ═══════════════════\n", name)
	}
	if !reachedFinalResponse {
		return "", fmt.Errorf("[%s] tool-use loop exhausted (%d rounds) without a final text response", name, maxToolRounds)
	}
	return lastRoundText.String(), nil
}

func (a *Agent) executeBash(ctx context.Context, rawInput json.RawMessage) (output string, isErr bool) {
	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return fmt.Sprintf("tool input parse error: %v", err), true
	}

	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "bash", "-c", input.Command)
	cmd.Dir = a.workDir
	cmd.Env = os.Environ()

	if a.verbose {
		// Tee: display output to a.out in real time and capture for Claude
		pr, pw := io.Pipe()
		cmd.Stdout = pw
		cmd.Stderr = pw

		var captured strings.Builder
		done := make(chan struct{})
		go func() {
			defer close(done)
			buf := make([]byte, 4096)
			for {
				n, err := pr.Read(buf)
				if n > 0 {
					chunk := buf[:n]
					a.out.Write(chunk) //nolint:errcheck
					captured.Write(chunk)
				}
				if err != nil {
					break
				}
			}
		}()

		runErr := cmd.Run()
		pw.Close()
		<-done

		out := captured.String()
		const maxOutput = 200_000
		if len(out) > maxOutput {
			out = out[:maxOutput] + "\n[output truncated]"
		}
		return out, runErr != nil
	}

	// Non-verbose: capture only, no output to terminal
	out, runErr := cmd.CombinedOutput()
	result := string(out)
	const maxOutput = 200_000
	if len(result) > maxOutput {
		result = result[:maxOutput] + "\n[output truncated]"
	}
	return result, runErr != nil
}

// renderMarkdown renders markdown text as formatted terminal output with a
// 4-space left margin. Falls back to plain text if rendering fails.
func renderMarkdown(w io.Writer, text string) {
	rendered, err := glamour.Render(text, "dark")
	if err != nil {
		fmt.Fprint(w, text)
		return
	}
	// Glamour's dark style already indents by 2; prepend 2 more for 4 total.
	for _, line := range strings.SplitAfter(rendered, "\n") {
		if strings.TrimRight(line, "\n\r") != "" {
			fmt.Fprint(w, "  "+line)
		} else {
			fmt.Fprint(w, line)
		}
	}
}

func bashToolParam() anthropic.ToolUnionParam {
	tool := anthropic.ToolParam{
		Name:        "bash",
		Description: param.NewOpt("Execute a bash command and return its combined stdout+stderr. Commands run in the repository root."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The bash command to execute",
				},
			},
			Required: []string{"command"},
		},
	}
	return anthropic.ToolUnionParam{OfTool: &tool}
}

// lineWriter prefixes every line of streamed text with a fixed prefix.
// dirtyLine is set when dots were printed directly to out; write() emits a \n
// before the next prefix so dots and text never share a line.
type lineWriter struct {
	out         io.Writer
	prefix      string
	atLineStart bool
	dirtyLine   bool
}

func newLineWriter(out io.Writer, prefix string) *lineWriter {
	return &lineWriter{out: out, prefix: prefix, atLineStart: true}
}

// markMidLine signals that content was written directly to out without a
// trailing newline, so the next write must open a fresh line first.
func (lw *lineWriter) markMidLine() {
	lw.dirtyLine = true
}

func (lw *lineWriter) write(text string) {
	for len(text) > 0 {
		if lw.atLineStart {
			if lw.dirtyLine {
				fmt.Fprintln(lw.out)
				lw.dirtyLine = false
			}
			fmt.Fprint(lw.out, lw.prefix)
			lw.atLineStart = false
		}
		idx := strings.IndexByte(text, '\n')
		if idx == -1 {
			fmt.Fprint(lw.out, text)
			return
		}
		fmt.Fprint(lw.out, text[:idx+1])
		text = text[idx+1:]
		lw.atLineStart = true
	}
}

// flush ensures any incomplete text line is terminated. It does not touch
// dirtyLine — dot accumulation is resolved by write() or the post-loop cleanup.
func (lw *lineWriter) flush() {
	if !lw.atLineStart {
		fmt.Fprintln(lw.out)
		lw.atLineStart = true
	}
}
