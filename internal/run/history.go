package run

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Summary is one line of history.jsonl — enough for the idle sparkline and the
// "3 held today" count, nothing that would let history grow unbounded.
type Summary struct {
	Branch   string        `json:"branch"`
	EndedAt  time.Time     `json:"ended_at"`
	Duration time.Duration `json:"duration"`
	Outcome  Phase         `json:"outcome"` // Done | Held | Failed
	Found    int           `json:"found"`
	Fixed    int           `json:"fixed"`
	CostUSD  float64       `json:"cost_usd"`
}

func historyPath(gitCommonDir string) string {
	return filepath.Join(Dir(gitCommonDir), "history.jsonl")
}

func AppendHistory(gitCommonDir string, s Summary) error {
	p := historyPath(gitCommonDir)
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

// History returns the last n summaries, oldest first (sparkline order).
func History(gitCommonDir string, n int) ([]Summary, error) {
	f, err := os.Open(historyPath(gitCommonDir))
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
