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
}

func newAgent(cfg Config) *Agent {
	return &Agent{
		client:    anthropic.NewClient(),
		model:     cfg.Model,
		maxTokens: cfg.MaxTokens,
		workDir:   cfg.WorkDir,
	}
}

// Run executes the skill identified by name with the given system prompt and user message.
// It streams Claude's text output to stdout and executes any bash tool calls.
func (a *Agent) Run(ctx context.Context, name, systemPrompt, userMessage string) error {
	fmt.Printf("\n╔═ [%s] ═══════════════════════\n", name)

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
			switch e := event.AsAny().(type) {
			case anthropic.ContentBlockDeltaEvent:
				switch d := e.Delta.AsAny().(type) {
				case anthropic.TextDelta:
					fmt.Print(d.Text)
				}
			}
		}
		if err := stream.Err(); err != nil {
			return fmt.Errorf("[%s] stream error (round %d): %w", name, round, err)
		}
		fmt.Println()

		if acc.StopReason != anthropic.StopReasonToolUse {
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

		messages = append(messages, anthropic.MessageParam{
			Role:    anthropic.MessageParamRoleUser,
			Content: resultContent,
		})
	}

	fmt.Printf("╚═ [%s] done ═══════════════════\n", name)
	return nil
}

func (a *Agent) executeBash(ctx context.Context, rawInput json.RawMessage) (output string, isErr bool) {
	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return fmt.Sprintf("tool input parse error: %v", err), true
	}

	fmt.Printf("\n$ %s\n", input.Command)

	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "bash", "-c", input.Command)
	cmd.Dir = a.workDir
	cmd.Env = os.Environ()

	// Tee output: display to terminal and capture for Claude
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
				os.Stdout.Write(chunk)
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
