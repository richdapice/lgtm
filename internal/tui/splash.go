package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The review gate is the one place you wait a minute with nothing to read.
// This is what's on screen then: the wordmark with a spectrum drifting across
// it and a bright diagonal gleam (a comet head over a colored tail), above and
// below it a band of faint diff dust — + and - twinkling on a seeded field.
// Same family as prism's splash; different weather.

var lgtmMark = []string{
	"██╗      ██████╗ ████████╗███╗   ███╗",
	"██║     ██╔════╝ ╚══██╔══╝████╗ ████║",
	"██║     ██║  ███╗   ██║   ██╔████╔██║",
	"██║     ██║   ██║   ██║   ██║╚██╔╝██║",
	"███████╗╚██████╔╝   ██║   ██║ ╚═╝ ██║",
	"╚══════╝ ╚═════╝    ╚═╝   ╚═╝     ╚═╝",
}

// spectrum runs green → teal → blue → violet: approval colors, no red.
var spectrum = []lipgloss.AdaptiveColor{
	{Light: "#1a7f37", Dark: "#3fb950"},
	{Light: "#1b7c83", Dark: "#39c5cf"},
	{Light: "#0969da", Dark: "#58a6ff"},
	{Light: "#6d28d9", Dark: "#a78bfa"},
	{Light: "#8250df", Dark: "#d2a8ff"},
	{Light: "#0969da", Dark: "#58a6ff"},
	{Light: "#1b7c83", Dark: "#39c5cf"},
}

var gleam = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#1f2328", Dark: "#f0f6fc"})

const (
	bandWidth   = 5
	gleamPeriod = 46
)

func markStyle(x, y, frame int) int {
	if g := ((x+y-frame*2)%gleamPeriod + gleamPeriod) % gleamPeriod; g < 2 {
		return -1
	}
	return ((x + frame) / bandWidth) % len(spectrum)
}

func renderMark(frame, width int) string {
	if width < 40 {
		var b strings.Builder
		for i, r := range "L G T M" {
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(spectrum[(i/2+frame/3)%len(spectrum)]).Render(string(r)))
		}
		return b.String()
	}
	var b strings.Builder
	for y, row := range lgtmMark {
		runes := []rune(row)
		start, cur := 0, markStyle(0, y, frame)
		flush := func(end int) {
			seg := string(runes[start:end])
			if cur < 0 {
				b.WriteString(gleam.Render(seg))
			} else {
				b.WriteString(lipgloss.NewStyle().Foreground(spectrum[cur]).Render(seg))
			}
		}
		for x := 1; x < len(runes); x++ {
			if idx := markStyle(x, y, frame); idx != cur {
				flush(x)
				start, cur = x, idx
			}
		}
		flush(len(runes))
		if y < len(lgtmMark)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// diffDust is the starfield with diff glyphs: a seeded LCG keeps positions
// stable between frames; each cell twinkles on its own phase, + in green and
// - in red at the peak, a faint dot otherwise.
func diffDust(frame, width, rows, seed int) string {
	rnd := uint32(seed)
	next := func() uint32 { rnd = rnd*1664525 + 1013904223; return rnd >> 8 }
	var lines []string
	for r := 0; r < rows; r++ {
		var b strings.Builder
		for x := 0; x < width; x++ {
			if next()%100 >= 8 {
				b.WriteByte(' ')
				continue
			}
			phase := float64(next()%628) / 100
			plus := next()%2 == 0
			switch tw := math.Sin(float64(frame)/3 + phase); {
			case tw > 0.7 && plus:
				b.WriteString(sGreen.Render("+"))
			case tw > 0.7:
				b.WriteString(sRed.Render("-"))
			case tw > -0.2:
				b.WriteString(sSubtle.Render("·"))
			default:
				b.WriteString(sFaint.Render("·"))
			}
		}
		lines = append(lines, b.String())
	}
	return strings.Join(lines, "\n")
}

// splash is the review-gate view: dust, mark, dust, then the review line.
func (m *model) splash() string {
	w := m.width
	dustW := w - 4
	if dustW > 60 {
		dustW = 60
	}
	blocks := []string{diffDust(m.frame, dustW, 2, 7), renderMark(m.frame, w), diffDust(m.frame, dustW, 2, 99)}
	art := lipgloss.JoinVertical(lipgloss.Center, blocks...)
	art = lipgloss.PlaceHorizontal(w, lipgloss.Center, art)
	var b strings.Builder
	b.WriteString("\n" + art + "\n\n")
	note := m.run.StepNote
	if note == "" {
		note = "reading the diff"
	}
	line := sAccent.Render(m.spin.View()) + " " + sSubtle.Render("review · "+note)
	if len(m.run.Lenses) > 0 && !m.run.Lenses[0].StartedAt.IsZero() {
		line += sFaint.Render("   " + m.run.Lenses[0].Elapsed(m.now()).Round(1e9).String())
	}
	b.WriteString(lipgloss.PlaceHorizontal(w, lipgloss.Center, line) + "\n")
	b.WriteString(lipgloss.PlaceHorizontal(w, lipgloss.Center, sFaint.Render("q cancel")) + "\n")
	return b.String()
}
