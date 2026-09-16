package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The review gate is the one place you wait a minute with nothing to read.
// What's on screen then: diff glyphs raining down in columns, and the wordmark
// decoding out of the noise cell by cell, with a glitch now and then — the run
// is cracking your diff, and it looks like it.

var lgtmMark = []string{
	"██╗      ██████╗ ████████╗███╗   ███╗",
	"██║     ██╔════╝ ╚══██╔══╝████╗ ████║",
	"██║     ██║  ███╗   ██║   ██╔████╔██║",
	"██║     ██║   ██║   ██║   ██║╚██╔╝██║",
	"███████╗╚██████╔╝   ██║   ██║ ╚═╝ ██║",
	"╚══════╝ ╚═════╝    ╚═╝   ╚═╝     ╚═╝",
}

var (
	rainHead   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#d2ffd8"})
	rainHi     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"})
	rainLo     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#1f6f2e"})
	rainDim    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#9ca3af", Dark: "#123d1c"})
	markOn     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"})
	markNoise  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#2a7a3b"})
	markGlitch = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#0d1117"}).
			Background(lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"})
)

const glyphs = "+-{}();=<>/*&|!01234567890abcdef#$%"

// hash is a small integer mixer so every cell gets its own stable randomness
// without allocating a generator per frame.
func hash(a, b, c int) uint32 {
	h := uint32(a)*0x9E3779B1 ^ uint32(b)*0x85EBCA77 ^ uint32(c)*0xC2B2AE3D
	h ^= h >> 15
	h *= 0x2C1B3C6D
	h ^= h >> 12
	return h
}

func glyph(h uint32) string { return string(glyphs[h%uint32(len(glyphs))]) }

// rain renders rows of falling columns. Each column has its own speed and
// phase; the head is bright, the tail fades over ~6 cells, and the glyphs
// churn as they fall.
func rain(frame, width, rows int, seed int) []string {
	out := make([]string, rows)
	for r := 0; r < rows; r++ {
		var b strings.Builder
		for x := 0; x < width; x++ {
			hc := hash(x, seed, 1)
			if hc%100 >= 45 { // ~45% of columns carry rain
				b.WriteByte(' ')
				continue
			}
			speed := int(hc%3) + 1
			period := rows + 8 + int(hc%9)
			head := (frame/speed + int(hc%uint32(period))) % period
			d := head - r
			g := glyph(hash(x, r, frame/2))
			switch {
			case d == 0:
				b.WriteString(rainHead.Render(g))
			case d > 0 && d < 3:
				b.WriteString(rainHi.Render(g))
			case d >= 3 && d < 6:
				b.WriteString(rainLo.Render(g))
			case d >= 6 && d < 8:
				b.WriteString(rainDim.Render(g))
			default:
				b.WriteByte(' ')
			}
		}
		out[r] = b.String()
	}
	return out
}

// decodeOrder gives each mark cell a rank; cells lock in rank order as the
// frame advances, so the letters resolve in a scrambled sweep rather than
// left to right.
func decodeOrder(x, y int) int { return int(hash(x, y, 7) % 1000) }

// renderMark draws the wordmark decoding out of noise. progress 0..1000 is
// how much has locked in; it climbs with the frame (about 17 seconds to
// full at the spinner's 12 ticks a second — a review takes a minute, and
// the reveal should feel earned) and holds there.
func renderMark(frame int) []string {
	progress := frame * 5
	if progress > 1000 {
		progress = 1000
	}
	glitchRow, glitchShift := -1, 0
	if progress >= 1000 && hash(frame/3, 0, 5)%9 == 0 {
		glitchRow = int(hash(frame, 1, 5) % uint32(len(lgtmMark)))
		glitchShift = int(hash(frame, 2, 5)%3) - 1
	}
	invertBand := progress >= 1000 && hash(frame/2, 0, 6)%23 == 0
	out := make([]string, len(lgtmMark))
	for y, row := range lgtmMark {
		runes := []rune(row)
		var b strings.Builder
		if y == glitchRow && glitchShift > 0 {
			b.WriteString(strings.Repeat(" ", glitchShift))
		}
		start := 0
		if y == glitchRow && glitchShift < 0 {
			start = -glitchShift
		}
		for x := start; x < len(runes); x++ {
			ch := runes[x]
			if ch == ' ' {
				b.WriteByte(' ')
				continue
			}
			switch {
			case invertBand && x/6%2 == 0:
				b.WriteString(markGlitch.Render(string(ch)))
			case decodeOrder(x, y) < progress:
				b.WriteString(markOn.Render(string(ch)))
			case hash(x, y, frame)%4 == 0:
				b.WriteByte(' ')
			default:
				b.WriteString(markNoise.Render(glyph(hash(x, y, frame))))
			}
		}
		out[y] = b.String()
	}
	return out
}

// splash is the review-gate view: rain above and below the decoding mark,
// then a terse log of what the run is doing.
func (m *model) splash() string {
	w := m.width
	rainW := w - 4
	if rainW > 72 {
		rainW = 72
	}
	var b strings.Builder
	b.WriteString("\n")
	for _, l := range rain(m.frame, rainW, 3, 11) {
		b.WriteString(lipgloss.PlaceHorizontal(w, lipgloss.Center, l) + "\n")
	}
	for _, l := range renderMark(m.frame) {
		b.WriteString(lipgloss.PlaceHorizontal(w, lipgloss.Center, l) + "\n")
	}
	for _, l := range rain(m.frame+17, rainW, 3, 23) {
		b.WriteString(lipgloss.PlaceHorizontal(w, lipgloss.Center, l) + "\n")
	}
	b.WriteString("\n")
	note := m.run.StepNote
	if note == "" {
		note = "reading the diff"
	}
	// typed log: characters appear a few per frame
	log1 := "> review · " + note
	log2 := "> " + m.run.Branch + " → " + m.run.Base
	shown := func(s string, at int) string {
		n := (m.frame - at) * 3
		if n < 0 {
			n = 0
		}
		if n > len([]rune(s)) {
			n = len([]rune(s))
		}
		cur := ""
		if n < len([]rune(s)) && m.frame%2 == 0 {
			cur = "█"
		}
		return string([]rune(s)[:n]) + cur
	}
	pad := (w - 44) / 2
	if pad < 1 {
		pad = 1
	}
	ind := strings.Repeat(" ", pad)
	b.WriteString(ind + rainHi.Render(shown(log2, 0)) + "\n")
	b.WriteString(ind + rainHi.Render(shown(log1, 8)))
	if len(m.run.Lenses) > 0 && !m.run.Lenses[0].StartedAt.IsZero() {
		b.WriteString(rainLo.Render("   " + m.run.Lenses[0].Elapsed(m.now()).Round(1e9).String()))
	}
	b.WriteString("\n" + ind + rainDim.Render("q cancel") + "\n")
	return b.String()
}
