package diffparse

// Commentable reports whether a review comment at (path, line, side) lands on
// a line present in the diff's hunks. GitHub rejects (422) comments outside
// hunks, so drafts are validated with this before submission.
func Commentable(files []FileDiff, path string, line int, side string) bool {
	for _, f := range files {
		if f.Path() != path {
			continue
		}
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if side == "RIGHT" && l.NewNum == line && l.Kind != Removed {
					return true
				}
				if side == "LEFT" && l.OldNum == line && l.Kind == Removed {
					return true
				}
			}
		}
	}
	return false
}

// NearestCommentable snaps a RIGHT-side line to the closest commentable new
// line in the same file, within maxDist. Returns 0 if none. Used to salvage
// AI drafts whose line numbers are slightly off.
func NearestCommentable(files []FileDiff, path string, line, maxDist int) int {
	best, bestDist := 0, maxDist+1
	for _, f := range files {
		if f.Path() != path {
			continue
		}
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if l.Kind == Removed || l.NewNum == 0 {
					continue
				}
				d := l.NewNum - line
				if d < 0 {
					d = -d
				}
				if d < bestDist {
					best, bestDist = l.NewNum, d
				}
			}
		}
	}
	return best
}
