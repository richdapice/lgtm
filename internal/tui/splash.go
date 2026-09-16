package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The review gate is the one place you wait a minute with nothing to read.
// What's on screen then is the stamp. LGTM is a rubber stamp on a pull
// request, so it arrives like one: it hovers hollow for half a second, drops,
// hits with a one-frame flash and a little ink bleed at the edges, and then it
// sits there — solid, unevenly inked the way a real stamp is — while the
// review line ticks underneath. The word is readable from the first frame.

var lgtmMark = []string{
	"██╗      ██████╗ ████████╗███╗   ███╗",
	"██║     ██╔════╝ ╚══██╔══╝████╗ ████║",
	"██║     ██║  ███╗   ██║   ██╔████╔██║",
	"██║     ██║   ██║   ██║   ██║╚██╔╝██║",
	"███████╗╚██████╔╝   ██║   ██║ ╚═╝ ██║",
	"╚══════╝ ╚═════╝    ╚═╝   ╚═╝     ╚═╝",
}

var (
	inkFull  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"})
	inkThin  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#2da44e", Dark: "#2ea043"})
	inkEdge  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#238636"})
	hollow   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#9ca3af", Dark: "#3d4450"})
	flash    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#1f2328", Dark: "#f0f6fc"})
	bleedHi  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#2ea043"})
	bleedLo  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#1b4d27"})
	stampDim = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#8b949e"})
)

// the stamp's timeline, in spinner ticks (~12/s)
const (
	hoverUntil = 6  // hollow, one row higher
	flashUntil = 8  // impact
	bleedUntil = 16 // ink spreading past the edges, fading
)

func hash(a, b, c int) uint32 {
	h := uint32(a)*0x9E3779B1 ^ uint32(b)*0x85EBCA77 ^ uint32(c)*0xC2B2AE3D
	h ^= h >> 15
	h *= 0x2C1B3C6D
	h ^= h >> 12
	return h
}

func isInk(r rune) bool  { return r == '█' }
func isEdge(r rune) bool { return strings.ContainsRune("╗║╝╔═╚", r) }

// renderStamp draws the mark for one frame of its timeline. It returns the
// rows plus one row of bleed above and below (blank when there is none).
func renderStamp(frame int) []string {
	w := len([]rune(lgtmMark[0]))
	var rows []string

	// bleed: cells just outside the ink that catch some, fading over the bleed window
	bleedRow := func(y int, rowAbove bool) string {
		if frame < flashUntil || frame >= bleedUntil {
			return strings.Repeat(" ", w+2)
		}
		age := frame - flashUntil // 0..7
		var b strings.Builder
		for x := -1; x <= w; x++ {
			src := y
			if rowAbove {
				src = 0
			} else {
				src = len(lgtmMark) - 1
			}
			near := false
			for dx := -1; dx <= 1; dx++ {
				if xx := x + dx; xx >= 0 && xx < w && isInk([]rune(lgtmMark[src])[xx]) {
					near = true
				}
			}
			h := hash(x, y, 3)
			if !near || h%100 >= 55-uint32(age)*6 {
				b.WriteByte(' ')
				continue
			}
			if age < 3 {
				b.WriteString(bleedHi.Render("▒"))
			} else {
				b.WriteString(bleedLo.Render("░"))
			}
		}
		return b.String()
	}

	rows = append(rows, bleedRow(-1, true))
	for y, row := range lgtmMark {
		runes := []rune(row)
		var b strings.Builder
		// side bleed
		side := func(x int) string {
			if frame < flashUntil || frame >= bleedUntil {
				return " "
			}
			nx := 0
			if x > 0 {
				nx = w - 1
			}
			if !isInk(runes[nx]) || hash(x, y, 5)%100 >= 40 {
				return " "
			}
			if frame-flashUntil < 3 {
				return bleedHi.Render("▒")
			}
			return bleedLo.Render("░")
		}
		b.WriteString(side(0))
		for x, r := range runes {
			switch {
			case r == ' ':
				b.WriteByte(' ')
			case frame < hoverUntil:
				// hollow: the ink is outlined, not filled
				if isInk(r) {
					b.WriteString(hollow.Render("░"))
				} else {
					b.WriteString(hollow.Render(string(r)))
				}
			case frame < flashUntil:
				b.WriteString(flash.Render(string(r)))
			default:
				// settled: solid, with the uneven inking of a real stamp that
				// drifts very slowly
				if isInk(r) {
					switch hash(x, y, frame/40) % 10 {
					case 0:
						b.WriteString(inkThin.Render("▓"))
					default:
						b.WriteString(inkFull.Render("█"))
					}
				} else if isEdge(r) {
					b.WriteString(inkEdge.Render(string(r)))
				} else {
					b.WriteString(inkFull.Render(string(r)))
				}
			}
		}
		b.WriteString(side(w))
		rows = append(rows, b.String())
	}
	rows = append(rows, bleedRow(len(lgtmMark), false))
	return rows
}

// splash is the review-gate view: the stamp, then the review line.
func (m *model) splash() string {
	w := m.width
	var b strings.Builder
	// the stamp hovers one row higher before it drops
	if m.frame < hoverUntil {
		b.WriteString("\n")
	} else {
		b.WriteString("\n\n")
	}
	for _, l := range renderStamp(m.frame) {
		b.WriteString(lipgloss.PlaceHorizontal(w, lipgloss.Center, l) + "\n")
	}
	if m.frame < hoverUntil {
		b.WriteString("\n")
	}
	note := m.run.StepNote
	if note == "" {
		note = "reading the diff"
	}
	line := sAccent.Render(m.spin.View()) + " " + stampDim.Render("review · "+note)
	if len(m.run.Lenses) > 0 && !m.run.Lenses[0].StartedAt.IsZero() {
		line += sFaint.Render("   " + m.run.Lenses[0].Elapsed(m.now()).Round(1e9).String())
	}
	b.WriteString("\n" + lipgloss.PlaceHorizontal(w, lipgloss.Center, line) + "\n")
	b.WriteString(lipgloss.PlaceHorizontal(w, lipgloss.Center, sFaint.Render("q cancel")) + "\n")
	return b.String()
}
