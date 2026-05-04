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
)

const maxToolRounds = 300

// Agent runs a Claude agent loop: sends messages, handles tool calls, streams text output.
type Agent struct {
	client    anthropic.Client
	model     string
	maxTokens int64
	workDir   string
	verbose   bool
	out       io.Writer
}

func newAgent(cfg Config, out io.Writer) *Agent {
	return &Agent{
		client:    anthropic.NewClient(),
		model:     cfg.Model,
		maxTokens: cfg.MaxTokens,
		workDir:   cfg.WorkDir,
		verbose:   cfg.Verbose,
		out:       out,
	}
}

// Run executes the skill identified by name with the given system prompt and user message.
// In normal mode: Claude text is printed prefixed with [name], bash I/O is hidden.
// In verbose mode: banners, raw Claude text, and full bash I/O are shown.
func (a *Agent) Run(ctx context.Context, name, systemPrompt, userMessage string) error {
	colorCode := agentColor(name)
	prefix := paint("["+name+"] ", colorCode)

	if a.verbose {
		fmt.Fprintf(a.out, "\n╔═ %s ═══════════════════════\n", paint("["+name+"]", colorCode))
	}

	lw := newLineWriter(a.out, prefix)

	messages := []anthropic.MessageParam{
		{
			Role: anthropic.MessageParamRoleUser,
			Content: []anthropic.ContentBlockParamUnion{
				{OfText: &anthropic.TextBlockParam{Text: userMessage}},
			},
		},
	}

	tools := []anthropic.ToolUnionParam{bashToolParam()}

	for round := 0; round < maxToolRounds; round++ {
		stream := a.client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
			Model:     a.model,
			MaxTokens: a.maxTokens,
			System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
			Messages:  messages,
			Tools:     tools,
		})

		var acc anthropic.Message
		for stream.Next() {
			event := stream.Current()
			if err := acc.Accumulate(event); err != nil {
				return fmt.Errorf("accumulate stream: %w", err)
			}
			if e, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
				if d, ok := e.Delta.AsAny().(anthropic.TextDelta); ok {
					if a.verbose {
						fmt.Fprint(a.out, d.Text)
					} else {
						lw.write(d.Text)
					}
				}
			}
		}
		if err := stream.Err(); err != nil {
			return fmt.Errorf("[%s] stream error (round %d): %w", name, round, err)
		}
		lw.flush() // ensure we end on a newline regardless of mode

		if acc.StopReason != anthropic.StopReasonToolUse {
			break
		}

		// Append the assistant turn to history
		messages = append(messages, acc.ToParam())

		// Execute all tool calls and collect results
		var resultContent []anthropic.ContentBlockParamUnion
		dotCount := 0
		for _, block := range acc.Content {
			if block.Type != "tool_use" {
				continue
			}
			if a.verbose {
				// Parse command for display
				var cmdInput struct {
					Command string `json:"command"`
				}
				json.Unmarshal(block.Input, &cmdInput) //nolint:errcheck
				fmt.Fprintf(a.out, "  $ %s\n", cmdInput.Command)
			} else {
				fmt.Fprint(a.out, dim("."))
				dotCount++
			}
			output, isErr := a.executeBash(ctx, block.Input)
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
		if dotCount > 0 {
			fmt.Fprintln(a.out)
		}

		messages = append(messages, anthropic.MessageParam{
			Role:    anthropic.MessageParamRoleUser,
			Content: resultContent,
		})
	}

	if a.verbose {
		fmt.Fprintf(a.out, "╚═ %s done ═══════════════════\n", paint("["+name+"]", colorCode))
	}
	return nil
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
type lineWriter struct {
	out         io.Writer
	prefix      string
	atLineStart bool
}

func newLineWriter(out io.Writer, prefix string) *lineWriter {
	return &lineWriter{out: out, prefix: prefix, atLineStart: true}
}

func (lw *lineWriter) write(text string) {
	for len(text) > 0 {
		if lw.atLineStart {
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

// flush ensures output ends on a newline (prevents a garbled prompt if the
// last delta had no trailing newline).
func (lw *lineWriter) flush() {
	if !lw.atLineStart {
		fmt.Fprintln(lw.out)
		lw.atLineStart = true
	}
}
