package finding

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// DismissFile is the committed ratchet: a finding you dismissed never comes back
// on any branch. It lives in the repo at .lgtm/dismissed.toml so the decision
// travels with the code and shows up in review like any other change.
const DismissFile = ".lgtm/dismissed.toml"

type Dismissal struct {
	ID     string    `toml:"id"`
	Rule   string    `toml:"rule"`
	Path   string    `toml:"path"`
	Reason string    `toml:"reason"`
	When   time.Time `toml:"when"`
}

type DismissList struct {
	Dismissed []Dismissal `toml:"dismissed"`
	ids       map[string]bool
}

// LoadDismissList reads the list from the repo root. A missing file is an empty
// list, not an error — most repos start with none.
func LoadDismissList(repoRoot string) (*DismissList, error) {
	var dl DismissList
	p := filepath.Join(repoRoot, DismissFile)
	if _, err := toml.DecodeFile(p, &dl); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			dl.ids = map[string]bool{}
			return &dl, nil
		}
		return nil, fmt.Errorf("finding: read %s: %w", DismissFile, err)
	}
	dl.ids = make(map[string]bool, len(dl.Dismissed))
	for _, d := range dl.Dismissed {
		dl.ids[d.ID] = true
	}
	return &dl, nil
}

func (dl *DismissList) Contains(id string) bool { return dl.ids[id] }

// Add records a dismissal. Idempotent by ID.
func (dl *DismissList) Add(f Finding, reason string) {
	if dl.ids[f.ID] {
		return
	}
	dl.Dismissed = append(dl.Dismissed, Dismissal{
		ID: f.ID, Rule: f.Rule, Path: f.Path, Reason: reason, When: time.Now().UTC(),
	})
	dl.ids[f.ID] = true
}

// Save writes the list back, creating .lgtm/ if needed.
func (dl *DismissList) Save(repoRoot string) error {
	p := filepath.Join(repoRoot, DismissFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintln(f, "# Findings dismissed for good. Managed by `lgtm dismiss`; hand edits are fine.")
	return toml.NewEncoder(f).Encode(dl)
}

// Prune drops every finding in s that is on the list. Called right after
// discover, before the set closes, so dismissed findings never count.
func (dl *DismissList) Prune(s *Set) int {
	kept := s.Findings[:0]
	n := 0
	for _, f := range s.Findings {
		if dl.Contains(f.ID) {
			n++
			continue
		}
		kept = append(kept, f)
	}
	s.Findings = kept
	s.reindex()
	return n
}
