// Package diffparse parses unified diffs (as produced by gh pr diff) keeping
// per-line old/new numbering, which review comment anchoring depends on.
package diffparse

import (
	"fmt"
	"strconv"
	"strings"
)

type LineKind int

const (
	Context LineKind = iota
	Added
	Removed
)

type Line struct {
	Kind   LineKind
	OldNum int // 0 when Kind == Added
	NewNum int // 0 when Kind == Removed
	Text   string
}

type Hunk struct {
	Header             string
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

type FileDiff struct {
	OldPath   string
	NewPath   string
	IsNew     bool
	IsDeleted bool
	IsBinary  bool
	IsRename  bool
	Hunks     []Hunk
}

// Path returns the path a reviewer would comment on (the new path, or the
// old one for deletions).
func (f FileDiff) Path() string {
	if f.IsDeleted {
		return f.OldPath
	}
	return f.NewPath
}

// Parse splits a unified diff into per-file diffs with line numbering.
// Unrecognized metadata lines are skipped rather than treated as errors so
// that format extensions don't break parsing.
func Parse(text string) ([]FileDiff, error) {
	var files []FileDiff
	var cur *FileDiff
	var hunk *Hunk
	var oldN, newN int
	var oldLeft, newLeft int // lines the current hunk still expects per side

	flushHunk := func() {
		if hunk != nil && cur != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		hunk = nil
	}

	for _, raw := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(raw, "diff --git "):
			flushHunk()
			if cur != nil {
				files = append(files, *cur)
			}
			cur = &FileDiff{}
			// "diff --git a/x b/y" — fallback paths; ---/+++ lines refine them.
			if parts := strings.SplitN(strings.TrimPrefix(raw, "diff --git "), " b/", 2); len(parts) == 2 {
				cur.OldPath = strings.TrimPrefix(parts[0], "a/")
				cur.NewPath = parts[1]
			}

		case cur == nil:
			continue // preamble before the first file

		case strings.HasPrefix(raw, "@@ "):
			flushHunk()
			var err error
			oldN, oldLeft, newN, newLeft, err = parseHunkHeader(raw)
			if err != nil {
				return nil, fmt.Errorf("file %s: %w", cur.Path(), err)
			}
			hunk = &Hunk{Header: raw, OldStart: oldN, OldCount: oldLeft, NewStart: newN, NewCount: newLeft}

		case hunk != nil && newLeft > 0 && strings.HasPrefix(raw, "+"):
			hunk.Lines = append(hunk.Lines, Line{Kind: Added, NewNum: newN, Text: raw[1:]})
			newN++
			newLeft--

		case hunk != nil && oldLeft > 0 && strings.HasPrefix(raw, "-"):
			hunk.Lines = append(hunk.Lines, Line{Kind: Removed, OldNum: oldN, Text: raw[1:]})
			oldN++
			oldLeft--

		case hunk != nil && oldLeft > 0 && newLeft > 0 && (strings.HasPrefix(raw, " ") || raw == ""):
			// A blank context line loses its leading space in some diffs.
			text := ""
			if raw != "" {
				text = raw[1:]
			}
			hunk.Lines = append(hunk.Lines, Line{Kind: Context, OldNum: oldN, NewNum: newN, Text: text})
			oldN++
			newN++
			oldLeft--
			newLeft--

		case hunk != nil && raw == `\ No newline at end of file`:
			continue

		case strings.HasPrefix(raw, "--- "):
			if p := strings.TrimPrefix(raw, "--- "); p == "/dev/null" {
				cur.IsNew = true
			} else {
				cur.OldPath = strings.TrimPrefix(p, "a/")
			}

		case strings.HasPrefix(raw, "+++ "):
			if p := strings.TrimPrefix(raw, "+++ "); p == "/dev/null" {
				cur.IsDeleted = true
			} else {
				cur.NewPath = strings.TrimPrefix(p, "b/")
			}

		case strings.HasPrefix(raw, "Binary files "), strings.HasPrefix(raw, "GIT binary patch"):
			cur.IsBinary = true

		case strings.HasPrefix(raw, "rename from "):
			cur.IsRename = true
			cur.OldPath = strings.TrimPrefix(raw, "rename from ")

		case strings.HasPrefix(raw, "rename to "):
			cur.NewPath = strings.TrimPrefix(raw, "rename to ")
		}
	}
	flushHunk()
	if cur != nil {
		files = append(files, *cur)
	}
	return files, nil
}

// parseHunkHeader extracts line ranges from "@@ -l[,n] +l[,n] @@ ...".
// A missing count means 1.
func parseHunkHeader(h string) (oldStart, oldCount, newStart, newCount int, err error) {
	malformed := fmt.Errorf("malformed hunk header %q", h)
	fields := strings.Fields(h)
	if len(fields) < 3 {
		return 0, 0, 0, 0, malformed
	}
	parse := func(s string) (start, count int, err error) {
		s = strings.TrimLeft(s, "-+")
		count = 1
		if i := strings.IndexByte(s, ','); i >= 0 {
			if count, err = strconv.Atoi(s[i+1:]); err != nil {
				return 0, 0, err
			}
			s = s[:i]
		}
		start, err = strconv.Atoi(s)
		return start, count, err
	}
	if oldStart, oldCount, err = parse(fields[1]); err != nil {
		return 0, 0, 0, 0, malformed
	}
	if newStart, newCount, err = parse(fields[2]); err != nil {
		return 0, 0, 0, 0, malformed
	}
	return oldStart, oldCount, newStart, newCount, nil
}
