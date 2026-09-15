// Package run is the on-disk state of one ceremony. It is written by the
// process doing the work and read by `lgtm statusline` on every refresh tick,
// so writes are atomic (temp + rename) and the file lives under the git common
// dir — shared by every worktree of the repo, never committed, gone with the
// clone.
package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/richdapice/lgtm/internal/finding"
)

// Phase is the coarse position in the ceremony. The status bar keys its
// layout off this.
type Phase string

const (
	Discover Phase = "discover"
	Fix      Phase = "fix"
	Verify   Phase = "verify"
	Held     Phase = "held" // waiting on a human
	PR       Phase = "pr"
	CI       Phase = "ci"
	Done     Phase = "done"
	Failed   Phase = "failed"
)

type LensState string

const (
	Pending    LensState = "pending"
	Running    LensState = "running"
	LensDone   LensState = "done"
	LensFailed LensState = "failed"
)

// Lens is one row in the bracket.
type Lens struct {
	Name      string    `json:"name"`
	Model     string    `json:"model"`
	State     LensState `json:"state"`
	Frac      float64   `json:"frac"` // 0..1, best-effort progress
	Found     int       `json:"found"`
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Error     string    `json:"error,omitempty"`
}

func (l Lens) Elapsed(now time.Time) time.Duration {
	if l.StartedAt.IsZero() {
		return 0
	}
	if !l.EndedAt.IsZero() {
		return l.EndedAt.Sub(l.StartedAt)
	}
	return now.Sub(l.StartedAt)
}

type PRInfo struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

type CIStatus struct {
	Total   int       `json:"total"`
	Passed  int       `json:"passed"`
	Failed  int       `json:"failed"`
	Pending int       `json:"pending"`
	Since   time.Time `json:"since"`
}

// Run is everything the bar needs and everything a resumed process needs.
type Run struct {
	Branch    string      `json:"branch"`
	Base      string      `json:"base"`
	Tree      string      `json:"tree"` // tree hash reviewed; a resume must match it
	Mode      string      `json:"mode"` // manual | auto
	Phase     Phase       `json:"phase"`
	StartedAt time.Time   `json:"started_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	Round     int         `json:"round"`
	MaxRounds int         `json:"max_rounds"`
	Lenses    []Lens      `json:"lenses"`
	Findings  finding.Set `json:"findings"`
	CostUSD   float64     `json:"cost_usd"`
	PR        *PRInfo     `json:"pr,omitempty"`
	CI        *CIStatus   `json:"ci,omitempty"`
	Error     string      `json:"error,omitempty"`
	PID       int         `json:"pid"` // so a stale file from a dead process is detectable
	// Decisions recorded by `lgtm decide` while held, consumed by `lgtm continue`.
	Decisions map[string]string `json:"decisions,omitempty"`
}

// Elapsed freezes once the run has stopped moving: a held run shows how long
// it took to get held, not how long you've been away.
func (r *Run) Elapsed(now time.Time) time.Duration {
	switch r.Phase {
	case Held, Done, Failed:
		if !r.UpdatedAt.IsZero() {
			return r.UpdatedAt.Sub(r.StartedAt)
		}
	}
	return now.Sub(r.StartedAt)
}

func (r *Run) Lens(name string) *Lens {
	for i := range r.Lenses {
		if r.Lenses[i].Name == name {
			return &r.Lenses[i]
		}
	}
	return nil
}

// Dir is <git-common-dir>/lgtm. Callers pass the common dir so this package
// stays free of git.
func Dir(gitCommonDir string) string { return filepath.Join(gitCommonDir, "lgtm") }

func runPath(gitCommonDir, branch string) string {
	// branch names contain slashes; keep them readable but flat
	return filepath.Join(Dir(gitCommonDir), "runs", strings.ReplaceAll(branch, "/", "%2F")+".json")
}

func New(branch, base, mode string, maxRounds int, lenses []string, models map[string]string) *Run {
	r := &Run{
		Branch: branch, Base: base, Mode: mode, Phase: Discover,
		StartedAt: time.Now().UTC(), MaxRounds: maxRounds, PID: os.Getpid(),
	}
	for _, l := range lenses {
		r.Lenses = append(r.Lenses, Lens{Name: l, Model: models[l], State: Pending})
	}
	return r
}

// Save writes atomically. The statusline may be mid-read on the old inode;
// rename guarantees it sees either the whole old file or the whole new one.
func (r *Run) Save(gitCommonDir string) error {
	r.UpdatedAt = time.Now().UTC()
	p := runPath(gitCommonDir, r.Branch)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func Load(gitCommonDir, branch string) (*Run, error) {
	b, err := os.ReadFile(runPath(gitCommonDir, branch))
	if err != nil {
		return nil, err
	}
	var r Run
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("run: %s: %w", branch, err)
	}
	return &r, nil
}

// Remove deletes the run file. Called when a branch's ceremony is fully done
// and its summary has been appended to history.
func Remove(gitCommonDir, branch string) error {
	err := os.Remove(runPath(gitCommonDir, branch))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// All returns every run on disk, newest update first — what the bar shows when
// several worktrees are in flight.
func All(gitCommonDir string) ([]*Run, error) {
	entries, err := os.ReadDir(filepath.Join(Dir(gitCommonDir), "runs"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var runs []*Run
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(Dir(gitCommonDir), "runs", e.Name()))
		if err != nil {
			continue
		}
		var r Run
		if json.Unmarshal(b, &r) == nil {
			runs = append(runs, &r)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].UpdatedAt.After(runs[j].UpdatedAt) })
	return runs, nil
}

// Alive reports whether the process that wrote the run still exists. A run
// whose writer died is shown as stale rather than as forever-running.
func (r *Run) Alive() bool {
	if r.PID <= 0 {
		return false
	}
	p, err := os.FindProcess(r.PID)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
