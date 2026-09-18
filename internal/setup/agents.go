package setup

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/richdapice/lgtm/internal/config"
)

// DetectAgents is every known recipe whose CLI is on PATH, in recipe order.
func DetectAgents() []config.Agent {
	var found []config.Agent
	for _, a := range config.Recipes() {
		if _, err := exec.LookPath(a.Command[0]); err == nil {
			found = append(found, a)
		}
	}
	return found
}

// PromptAgent shows the agents found and asks which one runs the show. pick
// is a name given on the command line and skips the question; yes takes the
// first one found. The second return is false when nothing was chosen.
func PromptAgent(in *bufio.Reader, out io.Writer, found []config.Agent, yes bool, pick string) (string, bool) {
	if len(found) == 0 {
		fmt.Fprintln(out, "\nno agent CLI on PATH (looked for claude, copilot, gemini, codex). Install one, then: lgtm init --agent NAME")
		return "", false
	}
	names := make([]string, len(found))
	for i, a := range found {
		names[i] = a.Name
	}
	has := func(n string) bool {
		for _, m := range names {
			if m == n {
				return true
			}
		}
		return false
	}
	if pick != "" {
		if !has(pick) {
			fmt.Fprintf(out, "\n%q isn't on PATH; found: %s\n", pick, strings.Join(names, ", "))
			return "", false
		}
		return pick, true
	}
	fmt.Fprintf(out, "\nagents on this machine: %s\n", strings.Join(names, ", "))
	if yes {
		return names[0], true
	}
	for {
		fmt.Fprintf(out, "which one reviews and fixes? [%s]: ", names[0])
		line, err := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return names[0], true
		}
		if has(line) {
			return line, true
		}
		fmt.Fprintf(out, "  not found on PATH; one of: %s\n", strings.Join(names, ", "))
		if err != nil { // stdin ran out; don't loop
			return names[0], true
		}
	}
}
