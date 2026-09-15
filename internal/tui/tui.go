// Package tui is the review surface: a compact panel drawn inline under your
// prompt — not a full-screen app. One finding at a time is in focus with the
// hunk it points at; the others are one line each; the action you're about to
// take is a visible highlight. Nothing is applied until Enter.
//
// The ceremony runs in a goroutine and never knows it has a screen: it asks a
// Decider and calls OnUpdate. The Decider here blocks on a channel until the
// human submits.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/richdapice/lgtm/internal/ceremony"
	"github.com/richdapice/lgtm/internal/diffparse"
	"github.com/richdapice/lgtm/internal/finding"
	"github.com/richdapice/lgtm/internal/run"
)

const maxWidth = 100

// Run prepares the ceremony, opens the panel, and drives it to completion.
// Everything the ceremony printed comes back as a transcript so the caller
// can put it on stdout after the panel closes.
func Run(ctx context.Context, o ceremony.Options) (transcript string, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	m := newModel(ctx, cancel)
	o.Out = m.out
	o.Decider = m.decider
	o.OnUpdate = func(r run.Run) { m.send(updateMsg{r}) }

	c, err := ceremony.Prepare(ctx, o)
	if err != nil {
		return "", err
	}
	m.files = c.Files()
	m.canFix = c.CanFix()
	m.decider.files = c.Files
	m.run = c.Snapshot()

	p := tea.NewProgram(m, tea.WithContext(ctx))
	m.program = p
	go func() { p.Send(doneMsg{c.Run(ctx)}) }()
	if _, perr := p.Run(); perr != nil && ctx.Err() == nil {
		return m.out.String(), perr
	}
	return m.out.String(), m.result
}

// ---- messages ----

type updateMsg struct{ run run.Run }
type doneMsg struct{ err error }
type noteMsg struct{ line string }

type decideMsg struct {
	open  []finding.Finding
	files []diffparse.FileDiff
	reply chan decideReply
}
type decideReply struct {
	decisions map[string]ceremony.Decision
	autopilot bool
	quit      bool
}

// ---- model ----

var actions = []struct {
	label string
	d     ceremony.Decision
	key   string
}{
	{"Fix", ceremony.Fix, "f"},
	{"Accept", ceremony.Accept, "a"},
	{"Dismiss", ceremony.Dismiss, "d"},
	{"Skip", ceremony.Skip, "s"},
}

type model struct {
	ctx     context.Context
	cancel  context.CancelFunc
	program *tea.Program
	out     *transcript
	decider *chanDecider

	run    run.Run
	files  []diffparse.FileDiff
	canFix bool
	notes  []string
	spin   spinner.Model
	width  int

	pending *decideMsg
	open    []finding.Finding
	marks   map[string]ceremony.Decision
	cursor  int // which finding
	action  int // which action is highlighted
	done    bool
	result  error
}

func newModel(ctx context.Context, cancel context.CancelFunc) *model {
	m := &model{ctx: ctx, cancel: cancel, marks: map[string]ceremony.Decision{}, width: 90}
	m.out = &transcript{m: m}
	m.decider = &chanDecider{m: m}
	m.spin = spinner.New(spinner.WithSpinner(spinner.MiniDot))
	return m
}

func (m *model) send(msg tea.Msg) {
	if m.program != nil {
		m.program.Send(msg)
	}
}

func (m *model) Init() tea.Cmd { return m.spin.Tick }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		if m.width > maxWidth {
			m.width = maxWidth
		}
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case updateMsg:
		m.run = msg.run
		return m, nil
	case noteMsg:
		m.notes = append(m.notes, msg.line)
		if len(m.notes) > 4 {
			m.notes = m.notes[len(m.notes)-4:]
		}
		return m, nil
	case decideMsg:
		m.pending = &msg
		m.open = msg.open
		if msg.files != nil {
			m.files = msg.files
		}
		m.marks = map[string]ceremony.Decision{}
		m.cursor, m.action = 0, 0
		if !m.canFix {
			m.action = 1
		}
		return m, nil
	case doneMsg:
		m.done = true
		m.result = msg.err
		return m, tea.Quit
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *model) allMarked() bool {
	for _, f := range m.open {
		if _, ok := m.marks[f.ID]; !ok {
			return false
		}
	}
	return len(m.open) > 0
}

func (m *model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		m.cancel()
		return m, tea.Quit
	}
	if m.pending == nil {
		if k.String() == "q" {
			m.cancel()
			return m, tea.Quit
		}
		return m, nil
	}
	switch k.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.open)-1 {
			m.cursor++
		}
	case "left", "h", "shift+tab":
		m.action = (m.action + len(actions) - 1) % len(actions)
		if !m.canFix && m.action == 0 {
			m.action = len(actions) - 1
		}
	case "right", "l", "tab":
		m.action = (m.action + 1) % len(actions)
		if !m.canFix && m.action == 0 {
			m.action = 1
		}
	case "f", "a", "d", "s":
		for i, a := range actions {
			if a.key == k.String() && (m.canFix || i != 0) {
				m.action = i
			}
		}
	case "enter":
		if m.allMarked() {
			m.submit(false, false)
			return m, nil
		}
		m.marks[m.open[m.cursor].ID] = actions[m.action].d
		// move to the next undecided finding, if there is one
		for i := 1; i <= len(m.open); i++ {
			j := (m.cursor + i) % len(m.open)
			if _, ok := m.marks[m.open[j].ID]; !ok {
				m.cursor = j
				break
			}
		}
	case "A":
		if m.canFix {
			for _, f := range m.open {
				if _, ok := m.marks[f.ID]; !ok {
					m.marks[f.ID] = ceremony.Fix
				}
			}
			m.submit(true, false)
		}
	case "q":
		m.submit(false, true)
	}
	return m, nil
}

func (m *model) submit(autopilot, quit bool) {
	if m.pending == nil {
		return
	}
	m.pending.reply <- decideReply{decisions: m.marks, autopilot: autopilot, quit: quit}
	m.pending = nil
	m.open = nil
}

// ---- view ----

var (
	accent = lipgloss.AdaptiveColor{Light: "#0969da", Dark: "#58a6ff"}
	subtle = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#8b949e"}
	faint  = lipgloss.AdaptiveColor{Light: "#9ca3af", Dark: "#565e68"}
	red    = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"}
	green  = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"}
	yellow = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"}
	selBg  = lipgloss.AdaptiveColor{Light: "#0969da", Dark: "#58a6ff"}
	selFg  = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#0d1117"}
	rowBg  = lipgloss.AdaptiveColor{Light: "#eaeef2", Dark: "#21262d"}

	sBold   = lipgloss.NewStyle().Bold(true)
	sAccent = lipgloss.NewStyle().Foreground(accent)
	sSubtle = lipgloss.NewStyle().Foreground(subtle)
	sFaint  = lipgloss.NewStyle().Foreground(faint)
	sRed    = lipgloss.NewStyle().Foreground(red)
	sGreen  = lipgloss.NewStyle().Foreground(green)
	sYellow = lipgloss.NewStyle().Foreground(yellow)
	sRow    = lipgloss.NewStyle().Background(rowBg)
	sSel    = lipgloss.NewStyle().Background(selBg).Foreground(selFg).Bold(true).Padding(0, 1)
	sOpt    = lipgloss.NewStyle().Foreground(subtle).Padding(0, 1)
	sOptOff = lipgloss.NewStyle().Foreground(faint).Padding(0, 1)
)

func (m *model) View() string {
	if m.done {
		return ""
	}
	var b strings.Builder
	b.WriteString(" " + m.header() + "\n")
	if m.pending == nil {
		b.WriteString(m.progress())
	} else {
		b.WriteString(m.review())
	}
	return b.String()
}

func (m *model) header() string {
	r := m.run
	h := sBold.Render("lgtm") + " ▸ " + sAccent.Render(r.Branch) + sFaint.Render(" → "+r.Base)
	switch {
	case m.pending != nil:
		h += "   " + sYellow.Render(fmt.Sprintf("%d need you", len(m.open)))
	case r.Phase != "":
		h += "   " + sAccent.Render(m.spin.View()) + " " + sSubtle.Render(string(r.Phase))
	}
	if r.Round > 0 {
		h += sFaint.Render(fmt.Sprintf("   round %d/%d", r.Round, r.MaxRounds))
	}
	h += sFaint.Render(fmt.Sprintf("   ≈$%.2f", r.CostUSD))
	return h
}

func (m *model) progress() string {
	var b strings.Builder
	for _, l := range m.run.Lenses {
		glyph := sFaint.Render("○")
		switch l.State {
		case run.Running:
			glyph = sAccent.Render(m.spin.View())
		case run.LensDone:
			glyph = sGreen.Render("✓")
		case run.LensFailed:
			glyph = sRed.Render("✗")
		}
		fmt.Fprintf(&b, "   %s %-12s %s\n", glyph, l.Name, sFaint.Render(l.Model))
	}
	for _, n := range m.notes {
		b.WriteString("   " + sSubtle.Render(clip(n, m.width-4)) + "\n")
	}
	b.WriteString(" " + sFaint.Render("q cancel") + "\n")
	return b.String()
}

func (m *model) review() string {
	var b strings.Builder
	w := m.width

	// the list: one line per finding, focused one highlighted
	for i, f := range m.open {
		glyph := sFaint.Render("·")
		if d, ok := m.marks[f.ID]; ok {
			glyph = sGreen.Render(actions[actionIndex(d)].key)
		}
		sev := sYellow.Render(fmt.Sprintf("%-5s", f.Severity))
		if f.Severity == finding.Block {
			sev = sRed.Render(fmt.Sprintf("%-5s", f.Severity))
		}
		loc := f.Path
		if f.Line > 0 {
			loc += fmt.Sprintf(":%d", f.Line)
		}
		rule := f.Rule
		if f.FixDeclined {
			rule += "  " + sYellow.Render("needs you")
		}
		line := fmt.Sprintf(" %s %s %-12s %s  %s", glyph, sev, sSubtle.Render(f.Lens), loc, sFaint.Render(rule))
		if i == m.cursor {
			line = sRow.Width(w - 1).Render(sAccent.Render("▸") + line[1:])
		} else {
			line = " " + line
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")

	// the focused finding: hunk, body, note
	f := m.open[m.cursor]
	lines, anchor := hunkAround(m.files, f.Path, f.Line, 4)
	for i, l := range lines {
		num := "    "
		if l.NewNum > 0 {
			num = fmt.Sprintf("%4d", l.NewNum)
		}
		text := clip(l.Text, w-10)
		switch l.Kind {
		case diffparse.Added:
			text = sGreen.Render("+ " + text)
		case diffparse.Removed:
			text = sRed.Render("- " + text)
		default:
			text = "  " + text
		}
		mark := " "
		if i == anchor {
			mark = sYellow.Render("▸")
		}
		fmt.Fprintf(&b, "  %s%s %s\n", mark, sFaint.Render(num), text)
	}
	if len(lines) > 0 {
		b.WriteString("\n")
	}
	body := lipgloss.NewStyle().Width(w - 4).Render(f.Body)
	b.WriteString(indent(body, "   ") + "\n")
	if f.Note != "" {
		note := lipgloss.NewStyle().Width(w - 6).Render(f.Note)
		b.WriteString(indent(sYellow.Render(note), "   ↳ ") + "\n")
	}
	b.WriteString("\n")

	// the action bar
	if m.allMarked() {
		b.WriteString("   " + sSel.Render("⏎ submit") + "   " + sFaint.Render("↑↓ change a decision · q hold") + "\n")
		return b.String()
	}
	b.WriteString("  ")
	for i, a := range actions {
		switch {
		case i == m.action:
			b.WriteString(sSel.Render(a.label))
		case i == 0 && !m.canFix:
			b.WriteString(sOptOff.Render(a.label))
		default:
			b.WriteString(sOpt.Render(a.label))
		}
		b.WriteString(" ")
	}
	b.WriteString("   " + sFaint.Render("↑↓ finding · ←→ action · ⏎ apply · A autopilot · q hold") + "\n")
	return b.String()
}

func actionIndex(d ceremony.Decision) int {
	for i, a := range actions {
		if a.d == d {
			return i
		}
	}
	return 0
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = prefix + lines[i]
		} else {
			lines[i] = strings.Repeat(" ", len([]rune(prefix))) + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// hunkAround returns up to ±n lines of the hunk containing newLine in path,
// and the index of the anchor line within the returned slice (-1 if none).
func hunkAround(files []diffparse.FileDiff, path string, newLine, n int) ([]diffparse.Line, int) {
	if newLine <= 0 {
		return nil, -1
	}
	for _, fd := range files {
		if fd.Path() != path {
			continue
		}
		for _, h := range fd.Hunks {
			for i, l := range h.Lines {
				if l.NewNum == newLine {
					lo, hi := i-n, i+n
					if lo < 0 {
						lo = 0
					}
					if hi >= len(h.Lines) {
						hi = len(h.Lines) - 1
					}
					return h.Lines[lo : hi+1], i - lo
				}
			}
		}
	}
	return nil, -1
}

func clip(s string, w int) string {
	if w <= 0 || len([]rune(s)) <= w {
		return s
	}
	return string([]rune(s)[:w-1]) + "…"
}

// ---- plumbing ----

type chanDecider struct {
	m     *model
	files func() []diffparse.FileDiff
}

func (d *chanDecider) Decide(ctx context.Context, open []finding.Finding, r *run.Run) (map[string]ceremony.Decision, bool, bool) {
	reply := make(chan decideReply, 1)
	var files []diffparse.FileDiff
	if d.files != nil {
		files = d.files()
	}
	d.m.send(decideMsg{open: open, files: files, reply: reply})
	select {
	case <-ctx.Done():
		return nil, false, true
	case rep := <-reply:
		return rep.decisions, rep.autopilot, rep.quit
	}
}

// transcript captures everything the ceremony prints. Lines show in the panel
// while it's up and are replayed to stdout afterwards.
type transcript struct {
	m   *model
	buf strings.Builder
}

func (t *transcript) Write(p []byte) (int, error) {
	t.buf.Write(p)
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			t.m.send(noteMsg{line})
		}
	}
	return len(p), nil
}

func (t *transcript) String() string { return t.buf.String() }
