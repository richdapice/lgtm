// Package agent runs a review CLI. The contract is the smallest one that works
// across tools: prompt on stdin, answer on stdout, argv from config. The prompt
// never goes on argv because a diff can exceed ARG_MAX.
//
// Two capability tiers exist because only some CLIs can take a JSON schema and
// hand back validated output. Where that exists we use it — it removes an
// entire class of parse bugs. Where it doesn't, the schema goes into the prompt
// and we extract leniently, which survives code fences and surrounding prose.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Capability int

const (
	// SchemaNative: the CLI accepts --json-schema and returns an envelope whose
	// structured_output field is already parsed and validated. Claude Code's -p
	// mode does this.
	SchemaNative Capability = iota
	// SchemaInPrompt: the schema is pasted into the prompt; the reply is
	// free text that should contain JSON somewhere.
	SchemaInPrompt
)

func ParseCapability(s string) (Capability, error) {
	switch s {
	case "native", "":
		return SchemaNative, nil
	case "prompt":
		return SchemaInPrompt, nil
	}
	return 0, fmt.Errorf("agent: unknown schema mode %q (want native or prompt)", s)
}

type Adapter struct {
	Name    string
	Command []string
	Model   string
	Cap     Capability
	Dir     string // working directory for the CLI; the repo root, so paths resolve

	// Native-tier only; prompt-tier CLIs have no equivalent flags.
	MaxBudgetUSD  float64
	FallbackModel string
}

// Result is what a call yields regardless of tier. Output is the structured
// payload — validated by the CLI on the native tier, extracted leniently on the
// prompt tier. Callers decode it into their own type and check it; that decode
// is the validation on the prompt tier.
type Result struct {
	Output   json.RawMessage
	Raw      string // full stdout, for logs
	CostUSD  float64
	Model    string
	Duration time.Duration
	NumTurns int
}

// Error carries the CLI's own classification when it gave one, so callers can
// tell "usage limit" from "bad prompt" from "it crashed".
type Error struct {
	Agent    string
	Kind     string // envelope subtype, or "exit", "empty", "no-json"
	Status   int    // api_error_status when present
	Stderr   string
	ExitCode int
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", e.Agent, e.Kind)
	if e.Status != 0 {
		fmt.Fprintf(&b, " (http %d)", e.Status)
	}
	if e.Stderr != "" {
		fmt.Fprintf(&b, ": %s", e.Stderr)
	}
	return b.String()
}

// envelope is the subset of claude -p --output-format json we rely on.
type envelope struct {
	IsError          bool            `json:"is_error"`
	Subtype          string          `json:"subtype"`
	APIErrorStatus   *int            `json:"api_error_status"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	Result           string          `json:"result"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	NumTurns         int             `json:"num_turns"`
	ModelUsage       map[string]struct {
		CanonicalModel string `json:"canonicalModel"`
	} `json:"modelUsage"`
}

func (a Adapter) argv(schema json.RawMessage) ([]string, error) {
	if len(a.Command) == 0 {
		return nil, errors.New("agent: command not configured")
	}
	args := append([]string(nil), a.Command[1:]...)
	if a.Model != "" {
		args = append(args, "--model", a.Model)
	}
	if a.Cap == SchemaNative {
		if len(schema) > 0 {
			args = append(args, "--json-schema", string(schema))
		}
		if a.MaxBudgetUSD > 0 {
			args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", a.MaxBudgetUSD))
		}
		if a.FallbackModel != "" {
			args = append(args, "--fallback-model", a.FallbackModel)
		}
	}
	return args, nil
}

// Ask runs one call. schema may be nil for free-text asks (Probe, PR body).
func (a Adapter) Ask(ctx context.Context, prompt string, schema json.RawMessage) (Result, error) {
	args, err := a.argv(schema)
	if err != nil {
		return Result{}, err
	}
	if a.Cap == SchemaInPrompt && len(schema) > 0 {
		prompt = wrapSchema(prompt, schema)
	}

	start := time.Now()
	cmd := exec.CommandContext(ctx, a.Command[0], args...)
	cmd.Dir = a.Dir
	cmd.Stdin = strings.NewReader(prompt)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	dur := time.Since(start)

	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	exit := 0
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		exit = ee.ExitCode()
	} else if runErr != nil {
		return Result{}, &Error{Agent: a.Name, Kind: "exec", Stderr: runErr.Error()}
	}

	res := Result{Raw: out.String(), Duration: dur}
	switch a.Cap {
	case SchemaNative:
		err = a.parseNative(&res, out.Bytes(), errb.String(), exit)
	default:
		err = a.parsePrompt(&res, out.Bytes(), errb.String(), exit, len(schema) > 0)
	}
	return res, err
}

// parseNative trusts the envelope over the exit code. A CLI can exit non-zero
// after producing a complete, successful result; throwing that away is the bug
// we are avoiding.
func (a Adapter) parseNative(res *Result, stdout []byte, stderr string, exit int) error {
	var env envelope
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &env); err != nil {
		if exit != 0 {
			return &Error{Agent: a.Name, Kind: "exit", ExitCode: exit, Stderr: strings.TrimSpace(stderr)}
		}
		return &Error{Agent: a.Name, Kind: "no-json", Stderr: firstLine(stdout)}
	}
	res.CostUSD = env.TotalCostUSD
	res.NumTurns = env.NumTurns
	for _, u := range env.ModelUsage {
		res.Model = u.CanonicalModel
		break
	}
	if env.IsError {
		e := &Error{Agent: a.Name, Kind: env.Subtype, ExitCode: exit, Stderr: strings.TrimSpace(stderr)}
		if e.Kind == "" {
			e.Kind = "error"
		}
		if env.APIErrorStatus != nil {
			e.Status = *env.APIErrorStatus
		}
		if e.Stderr == "" {
			e.Stderr = env.Result
		}
		return e
	}
	if len(env.StructuredOutput) > 0 && string(env.StructuredOutput) != "null" {
		res.Output = env.StructuredOutput
		return nil
	}
	if env.Result != "" {
		res.Output = json.RawMessage(strconv(env.Result))
		return nil
	}
	return &Error{Agent: a.Name, Kind: "empty", ExitCode: exit}
}

func (a Adapter) parsePrompt(res *Result, stdout []byte, stderr string, exit int, wantJSON bool) error {
	reply := bytes.TrimSpace(stdout)
	if len(reply) == 0 {
		if exit != 0 {
			return &Error{Agent: a.Name, Kind: "exit", ExitCode: exit, Stderr: strings.TrimSpace(stderr)}
		}
		return &Error{Agent: a.Name, Kind: "empty"}
	}
	if !wantJSON {
		res.Output = json.RawMessage(strconv(string(reply)))
		return nil
	}
	obj, ok := ExtractJSON(reply)
	if !ok {
		return &Error{Agent: a.Name, Kind: "no-json", ExitCode: exit, Stderr: firstLine(reply)}
	}
	res.Output = obj
	return nil
}

// ExtractJSON finds the outermost JSON object or array in free text: first
// opening bracket to the last matching closer. Code fences and prose around it
// are ignored. It does not attempt to repair truncated JSON.
func ExtractJSON(b []byte) (json.RawMessage, bool) {
	start := -1
	var closer byte
	for i, c := range b {
		if c == '{' || c == '[' {
			start = i
			if c == '{' {
				closer = '}'
			} else {
				closer = ']'
			}
			break
		}
	}
	if start < 0 {
		return nil, false
	}
	end := bytes.LastIndexByte(b, closer)
	if end <= start {
		return nil, false
	}
	cand := b[start : end+1]
	if !json.Valid(cand) {
		return nil, false
	}
	return json.RawMessage(cand), true
}

func wrapSchema(prompt string, schema json.RawMessage) string {
	return prompt + "\n\nRespond with ONLY a JSON value matching this schema. No prose, no code fences.\n" +
		"Schema:\n" + string(schema) + "\n"
}

// Probe checks the CLI is on PATH and answers. Costs one real request; the
// startup doctor calls it once, not on every run.
func (a Adapter) Probe(ctx context.Context) error {
	if len(a.Command) == 0 {
		return errors.New("agent: command not configured")
	}
	if _, err := exec.LookPath(a.Command[0]); err != nil {
		return fmt.Errorf("agent: %s not found on PATH", a.Command[0])
	}
	res, err := a.Ask(ctx, "Reply with exactly the two letters OK and nothing else.", nil)
	if err != nil {
		return err
	}
	var s string
	if json.Unmarshal(res.Output, &s) != nil {
		s = string(res.Output)
	}
	if !strings.Contains(strings.ToUpper(s), "OK") {
		return fmt.Errorf("agent: %s answered %q, expected OK", a.Name, firstLine([]byte(s)))
	}
	return nil
}

// Text is the reply as plain text when no schema was requested.
func (r Result) Text() string {
	var s string
	if json.Unmarshal(r.Output, &s) == nil {
		return s
	}
	return string(r.Output)
}


func strconv(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
