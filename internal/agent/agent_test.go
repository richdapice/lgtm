package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeCLI writes a shell script that prints the given stdout and exits with
// code, so envelope parsing is tested without spending a token.
func fakeCLI(t *testing.T, stdout string, code int) []string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake")
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s' " + shellQuote(stdout) + "\nexit " + itoa(code) + "\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{p}
}

func shellQuote(s string) string { return "'" + replaceAll(s, "'", `'\''`) + "'" }
func replaceAll(s, old, new string) string {
	out := ""
	for i := 0; i < len(s); i++ {
		if len(s)-i >= len(old) && s[i:i+len(old)] == old {
			out += new
			i += len(old) - 1
		} else {
			out += string(s[i])
		}
	}
	return out
}
func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

var schema = json.RawMessage(`{"type":"object","properties":{"findings":{"type":"array"}},"required":["findings"]}`)

func TestNativeHappyPath(t *testing.T) {
	env := `{"is_error":false,"subtype":"success","total_cost_usd":0.043,"num_turns":4,
	  "modelUsage":{"claude-haiku-4-5-20251001":{"canonicalModel":"claude-haiku-4-5"}},
	  "result":"{\"findings\":[]}","structured_output":{"findings":[{"file":"x.ts"}]}}`
	a := Adapter{Name: "fake", Command: fakeCLI(t, env, 0), Cap: SchemaNative}
	res, err := a.Ask(context.Background(), "p", schema)
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Findings []struct{ File string } }
	if err := json.Unmarshal(res.Output, &out); err != nil || len(out.Findings) != 1 || out.Findings[0].File != "x.ts" {
		t.Fatalf("output = %s (%v)", res.Output, err)
	}
	if res.CostUSD != 0.043 || res.Model != "claude-haiku-4-5" || res.NumTurns != 4 {
		t.Fatalf("meta = %+v", res)
	}
}

func TestNativeSuccessDespiteNonZeroExit(t *testing.T) {
	env := `{"is_error":false,"subtype":"success","structured_output":{"findings":[]}}`
	a := Adapter{Name: "fake", Command: fakeCLI(t, env, 1), Cap: SchemaNative}
	res, err := a.Ask(context.Background(), "p", schema)
	if err != nil {
		t.Fatalf("complete result discarded because of exit code: %v", err)
	}
	if string(res.Output) != `{"findings":[]}` {
		t.Fatalf("output = %s", res.Output)
	}
}

func TestNativeErrorEnvelopeIsClassified(t *testing.T) {
	env := `{"is_error":true,"subtype":"error_max_budget","api_error_status":429,"result":"budget exceeded"}`
	a := Adapter{Name: "fake", Command: fakeCLI(t, env, 0), Cap: SchemaNative}
	_, err := a.Ask(context.Background(), "p", schema)
	var ae *Error
	if !errors.As(err, &ae) || ae.Kind != "error_max_budget" || ae.Status != 429 || ae.Stderr != "budget exceeded" {
		t.Fatalf("err = %#v", err)
	}
}

func TestNativeGarbageWithExitIsExitError(t *testing.T) {
	a := Adapter{Name: "fake", Command: fakeCLI(t, "segfault lol", 139), Cap: SchemaNative}
	_, err := a.Ask(context.Background(), "p", schema)
	var ae *Error
	if !errors.As(err, &ae) || ae.Kind != "exit" || ae.ExitCode != 139 {
		t.Fatalf("err = %#v", err)
	}
}

func TestPromptTierExtractsFromProse(t *testing.T) {
	reply := "Sure! Here are the findings:\n```json\n{\"findings\":[{\"file\":\"a.ts\"}]}\n```\nLet me know if you need more."
	a := Adapter{Name: "fake", Command: fakeCLI(t, reply, 0), Cap: SchemaInPrompt}
	res, err := a.Ask(context.Background(), "p", schema)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Output) != `{"findings":[{"file":"a.ts"}]}` {
		t.Fatalf("output = %s", res.Output)
	}
}

func TestPromptTierNoJSON(t *testing.T) {
	a := Adapter{Name: "fake", Command: fakeCLI(t, "I couldn't review that.", 0), Cap: SchemaInPrompt}
	_, err := a.Ask(context.Background(), "p", schema)
	var ae *Error
	if !errors.As(err, &ae) || ae.Kind != "no-json" {
		t.Fatalf("err = %#v", err)
	}
}

func TestArgvPerTier(t *testing.T) {
	native := Adapter{Command: []string{"claude", "-p"}, Model: "opus", Cap: SchemaNative, MaxBudgetUSD: 0.5, FallbackModel: "sonnet"}
	args, _ := native.argv(schema)
	want := []string{"-p", "--model", "opus", "--json-schema", string(schema), "--max-budget-usd", "0.50", "--fallback-model", "sonnet"}
	if len(args) != len(want) {
		t.Fatalf("native argv = %v", args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("native argv[%d] = %q, want %q", i, args[i], want[i])
		}
	}
	prompt := Adapter{Command: []string{"copilot", "-s"}, Model: "gpt", Cap: SchemaInPrompt, MaxBudgetUSD: 0.5}
	args, _ = prompt.argv(schema)
	if len(args) != 3 || args[2] != "gpt" {
		t.Fatalf("prompt argv leaked native flags: %v", args)
	}
}

func TestExtractJSONTruncatedIsRejected(t *testing.T) {
	if _, ok := ExtractJSON([]byte(`{"findings":[{"file":"a.ts"`)); ok {
		t.Fatal("truncated JSON accepted")
	}
}
