package setup

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richdapice/lgtm/internal/config"
)

func TestDetectAgentsFindsWhatIsOnPath(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "copilot"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin)
	found := DetectAgents()
	if len(found) != 1 || found[0].Name != "copilot" || found[0].Schema != "prompt" {
		t.Fatalf("found = %+v", found)
	}
}

func TestPromptAgentDefaultsPicksAndValidates(t *testing.T) {
	found := []config.Agent{{Name: "claude"}, {Name: "copilot"}}
	var out strings.Builder
	if n, ok := PromptAgent(bufio.NewReader(strings.NewReader("\n")), &out, found, false, ""); !ok || n != "claude" {
		t.Fatalf("Enter should take the first: %q %v", n, ok)
	}
	if n, _ := PromptAgent(bufio.NewReader(strings.NewReader("copilot\n")), &out, found, false, ""); n != "copilot" {
		t.Fatalf("typed name: %q", n)
	}
	if n, _ := PromptAgent(bufio.NewReader(strings.NewReader("gemini\ncopilot\n")), &out, found, false, ""); n != "copilot" {
		t.Fatalf("unknown name should re-ask: %q", n)
	}
	if n, _ := PromptAgent(bufio.NewReader(strings.NewReader("")), &out, found, true, ""); n != "claude" {
		t.Fatalf("-y: %q", n)
	}
	if _, ok := PromptAgent(bufio.NewReader(strings.NewReader("")), &out, found, false, "gemini"); ok {
		t.Fatal("--agent with a CLI not on PATH must fail")
	}
	if _, ok := PromptAgent(bufio.NewReader(strings.NewReader("")), &out, nil, true, ""); ok {
		t.Fatal("nothing on PATH must not pick")
	}
}

func TestWriteGlobalRoundTrip(t *testing.T) {
	t.Setenv("LGTM_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	g := &config.Global{DefaultAgent: "copilot", Agents: config.Recipes()[:2]}
	if err := config.WriteGlobal(g); err != nil {
		t.Fatal(err)
	}
	back, err := config.LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if back.DefaultAgent != "copilot" || len(back.Agents) != 2 || back.Agents[1].FixCommand[len(back.Agents[1].FixCommand)-1] != "write" {
		t.Fatalf("round trip = %+v", back)
	}
}
