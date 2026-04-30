package autoresearch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Suite describes a benchmark suite for one skill.
type Suite struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	SkillPath   string `json:"skill_path" yaml:"skill_path"`
	Cases       []Case `json:"cases" yaml:"cases"`
}

// Case describes one benchmark prompt and its scoring rubric.
type Case struct {
	ID          string            `json:"id" yaml:"id"`
	Title       string            `json:"title" yaml:"title"`
	Prompt      string            `json:"prompt" yaml:"prompt"`
	JudgeRubric string            `json:"judge_rubric,omitempty" yaml:"judge_rubric,omitempty"`
	Variables   map[string]string `json:"variables,omitempty" yaml:"variables,omitempty"`
	Criteria    []Criterion       `json:"criteria" yaml:"criteria"`
}

// Criterion is a deterministic check over the final answer, command list, tool
// results, or all transcript text. It is intentionally simple so new benchmark
// cases can be added without writing Go code.
type Criterion struct {
	Name            string  `json:"name" yaml:"name"`
	Source          string  `json:"source" yaml:"source"` // final, commands, tool_results, transcript
	Contains        string  `json:"contains,omitempty" yaml:"contains,omitempty"`
	Regex           string  `json:"regex,omitempty" yaml:"regex,omitempty"`
	Not             bool    `json:"not,omitempty" yaml:"not,omitempty"`
	CaseInsensitive bool    `json:"case_insensitive,omitempty" yaml:"case_insensitive,omitempty"`
	Points          float64 `json:"points" yaml:"points"`
}

// ToolCall captures a tool invocation from pi's JSON event stream.
type ToolCall struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Args     json.RawMessage `json:"args,omitempty"`
	Command  string          `json:"command,omitempty"`
	Result   string          `json:"result,omitempty"`
	IsError  bool            `json:"is_error"`
	Duration string          `json:"duration,omitempty"`
}

// CriterionResult records whether one rubric criterion passed.
type CriterionResult struct {
	Name   string  `json:"name"`
	Passed bool    `json:"passed"`
	Points float64 `json:"points"`
	Max    float64 `json:"max"`
	Detail string  `json:"detail,omitempty"`
}

// JudgeResult is populated when skillbench runs an optional LLM-as-judge pass.
type JudgeResult struct {
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
	Raw    string  `json:"raw,omitempty"`
}

// CaseResult contains all data needed to audit one case.
type CaseResult struct {
	ID                    string            `json:"id"`
	Title                 string            `json:"title"`
	Prompt                string            `json:"prompt"`
	Score                 float64           `json:"score"`
	MaxScore              float64           `json:"max_score"`
	NormalizedScore       float64           `json:"normalized_score"`
	DeterministicScore    float64           `json:"deterministic_score"`
	DeterministicMaxScore float64           `json:"deterministic_max_score"`
	FinalAnswer           string            `json:"final_answer"`
	Commands              []string          `json:"commands"`
	ToolCalls             []ToolCall        `json:"tool_calls"`
	Criteria              []CriterionResult `json:"criteria"`
	Judge                 *JudgeResult      `json:"judge,omitempty"`
	RawJSONLPath          string            `json:"raw_jsonl_path,omitempty"`
	Error                 string            `json:"error,omitempty"`
	StartedAt             time.Time         `json:"started_at"`
	CompletedAt           time.Time         `json:"completed_at"`
	WallClockDuration     string            `json:"wall_clock_duration"`
}

// SuiteResult is the machine-readable benchmark report.
type SuiteResult struct {
	SuiteName         string       `json:"suite_name"`
	Description       string       `json:"description"`
	Mode              string       `json:"mode"`
	Model             string       `json:"model"`
	SkillPath         string       `json:"skill_path"`
	CasesPath         string       `json:"cases_path"`
	RepoRoot          string       `json:"repo_root"`
	Score             float64      `json:"score"`
	MaxScore          float64      `json:"max_score"`
	NormalizedScore   float64      `json:"normalized_score"`
	Cases             []CaseResult `json:"cases"`
	StartedAt         time.Time    `json:"started_at"`
	CompletedAt       time.Time    `json:"completed_at"`
	WallClockDuration string       `json:"wall_clock_duration"`
}

// LoadSuite reads a YAML benchmark suite.
func LoadSuite(path string) (Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Suite{}, err
	}
	var suite Suite
	if err := yaml.Unmarshal(data, &suite); err != nil {
		return Suite{}, err
	}
	if suite.Name == "" {
		return Suite{}, fmt.Errorf("suite name is required")
	}
	if len(suite.Cases) == 0 {
		return Suite{}, fmt.Errorf("suite %q has no cases", suite.Name)
	}
	for i, tc := range suite.Cases {
		if tc.ID == "" {
			return Suite{}, fmt.Errorf("case %d is missing id", i)
		}
		if tc.Prompt == "" {
			return Suite{}, fmt.Errorf("case %q is missing prompt", tc.ID)
		}
		if len(tc.Criteria) == 0 {
			return Suite{}, fmt.Errorf("case %q has no criteria", tc.ID)
		}
	}
	return suite, nil
}

// WriteJSON writes v as pretty JSON, creating parent directories.
func WriteJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// RepoRoot returns the git repository root, falling back to cwd.
func RepoRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err == nil {
		root := strings.TrimSpace(out.String())
		if root != "" {
			return root, nil
		}
	}
	return os.Getwd()
}

// AbsFromRoot returns path if absolute, otherwise root/path.
func AbsFromRoot(root, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(root, path))
}

// Variables returns the default benchmark template variables.
func Variables(root, skillPath string) map[string]string {
	autoDir := filepath.Join(root, "auto-improve-skills")
	benchDir := RemoteHostDiagnosticsBenchmarkDir(root)
	fixtureRoot := RemoteHostDiagnosticsGeneratedFixtureRoot(root)
	return map[string]string{
		"ROOT":           root,
		"AUTO_DIR":       autoDir,
		"BENCH_DIR":      benchDir,
		"SKILL_PATH":     skillPath,
		"LOG_ROOT":       filepath.Join(fixtureRoot, "logs"),
		"EMPTY_LOG_ROOT": filepath.Join(fixtureRoot, "container", "var", "log"),
		"HOST_LOG_ROOT":  filepath.Join(fixtureRoot, "container", "host", "var", "log"),
	}
}

// Expand replaces {{NAME}} placeholders with values.
func Expand(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

// MergeVariables returns defaults overlaid with case-specific variables.
func MergeVariables(defaults map[string]string, extra map[string]string) map[string]string {
	merged := make(map[string]string, len(defaults)+len(extra))
	for k, v := range defaults {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = Expand(v, merged)
	}
	return merged
}
