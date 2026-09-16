package run

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Summary is one line of history.jsonl — enough for the idle sparkline and the
// "3 held today" count, nothing that would let history grow unbounded.
type Summary struct {
	Repo     string        `json:"repo,omitempty"` // git common dir, so a run is traceable to its repo
	Branch   string        `json:"branch"`
	EndedAt  time.Time     `json:"ended_at"`
	Duration time.Duration `json:"duration"`
	Outcome  Phase         `json:"outcome"` // Done | Held | Failed
	Found    int           `json:"found"`
	Fixed    int           `json:"fixed"`
	CostUSD  float64       `json:"cost_usd"`
}

// HistoryPath is user-level, not per-repo: "12 runs today" and your streak
// are about you, and should read the same from every session and every repo.
// $LGTM_STATE overrides; else $XDG_STATE_HOME/lgtm; else ~/.local/state/lgtm.
func HistoryPath() string {
	if p := os.Getenv("LGTM_STATE"); p != "" {
		return filepath.Join(p, "history.jsonl")
	}
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "lgtm", "history.jsonl")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "history.jsonl"
	}
	return filepath.Join(home, ".local", "state", "lgtm", "history.jsonl")
}

func AppendHistory(gitCommonDir string, s Summary) error {
	if s.Repo == "" {
		s.Repo = gitCommonDir
	}
	p := HistoryPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// History returns your last n runs across every repo, oldest first (sparkline
// order). gitCommonDir is accepted for symmetry with AppendHistory and ignored.
func History(gitCommonDir string, n int) ([]Summary, error) {
	f, err := os.Open(HistoryPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var all []Summary
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var s Summary
		if json.Unmarshal(sc.Bytes(), &s) == nil {
			all = append(all, s)
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, sc.Err()
}

// reviewedPath lists tree hashes a completed run has already reviewed, so a
// second push of the same tree doesn't pay for a second review.
func reviewedPath(gitCommonDir string) string { return filepath.Join(Dir(gitCommonDir), "reviewed") }

func Reviewed(gitCommonDir, tree string) bool {
	b, err := os.ReadFile(reviewedPath(gitCommonDir))
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == tree {
			return true
		}
	}
	return false
}

func MarkReviewed(gitCommonDir, tree string) error {
	if err := os.MkdirAll(Dir(gitCommonDir), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(reviewedPath(gitCommonDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, tree)
	return err
}
