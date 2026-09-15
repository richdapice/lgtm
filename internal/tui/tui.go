// Package tui is the review surface: findings on the left, the hunk each one
// points at on the right, single-key decisions. The ceremony runs in a
// goroutine and talks to the screen through messages — it never knows it has a
// UI, it just asks a Decider and calls OnUpdate. The Decider here blocks on a
// channel until the human submits.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/richdapice/lgtm/internal/ceremony"
	"github.com/richdapice/lgtm/internal/diffparse"
	"github.com/richdapice/lgtm/internal/finding"
	"github.com/richdapice/lgtm/internal/run"
)

// Run prepares the ceremony, opens the screen, and drives it to completion.
// Everything the ceremony printed comes back as a transcript so the caller
// can put it on stdout after the screen closes.
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

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
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

// decideMsg carries the ceremony's question; the model answers on reply.
type decideMsg struct {
	open  []finding.Finding
	files []diffparse.FileDiff // the diff as of this round; fix rounds rewrite it
	reply chan decideReply
}
type decideReply struct {
	decisions map[string]ceremony.Decision
	autopilot bool
	quit      bool
}

// ---- model ----

type model struct {
	ctx     context.Context
	cancel  context.CancelFunc
	program *tea.Program
	out     *transcript
	decider *chanDecider

	run     run.Run
	files   []diffparse.FileDiff
	canFix  bool
	notes   []string
	spin    spinner.Model
	width   int
	height  int
	diff    viewport.Model
	pending *decideMsg
	open    []finding.Finding
	marks   map[string]ceremony.Decision
	cursor  int
	done    bool
	result  error
}

func newModel(ctx context.Context, cancel context.CancelFunc) *model {
	m := &model{ctx: ctx, cancel: cancel, marks: map[string]ceremony.Decision{}}
	m.out = &transcript{m: m}
	m.decider = &chanDecider{m: m}
	m.spin = spinner.New(spinner.WithSpinner(spinner.MiniDot))
	m.diff = viewport.New(0, 0)
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
		m.width, m.height = msg.Width, msg.Height
		m.layout()
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
		if len(m.notes) > 6 {
			m.notes = m.notes[len(m.notes)-6:]
		}
		return m, nil
	case decideMsg:
		m.pending = &msg
		m.open = msg.open
		if msg.files != nil {
			m.files = msg.files
		}
		m.marks = map[string]ceremony.Decision{}
		m.cursor = 0
		m.renderDiff()
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
	cur := m.open[m.cursor]
	mark := func(d ceremony.Decision) {
		m.marks[cur.ID] = d
		if m.cursor < len(m.open)-1 {
			m.cursor++
			m.renderDiff()
		}
	}
	switch k.String() {
	case "j", "down":
		if m.cursor < len(m.open)-1 {
			m.cursor++
			m.renderDiff()
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
			m.renderDiff()
		}
	case "f":
		if m.canFix {
			mark(ceremony.Fix)
		} else {
			m.notes = append(m.notes, "no fix_command configured for this agent")
		}
	case "a":
		mark(ceremony.Accept)
	case "d":
		mark(ceremony.Dismiss)
	case "s":
		mark(ceremony.Skip)
	case "A":
		if m.canFix {
			for _, f := range m.open {
				if _, ok := m.marks[f.ID]; !ok {
					m.marks[f.ID] = ceremony.Fix
				}
			}
			m.submit(true, false)
		}
	case "enter":
		m.submit(false, false)
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

func (m *model) layout() {
	m.diff.Width = m.width - m.listWidth() - 3
	m.diff.Height = m.height - 5
	m.renderDiff()
}

func (m *model) listWidth() int {
	w := m.width * 2 / 5
	if w < 34 {
		w = 34
	}
	if w > 56 {
		w = 56
	}
	return w
}

// ---- view ----

var (
	accent = lipgloss.AdaptiveColor{Light: "#0969da", Dark: "#58a6ff"}
	subtle = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#8b949e"}
	faint  = lipgloss.AdaptiveColor{Light: "#9ca3af", Dark: "#565e68"}
	red    = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"}
	green  = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"}
	yellow = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"}
	curBg  = lipgloss.AdaptiveColor{Light: "#eaeef2", Dark: "#30363d"}

	sBold   = lipgloss.NewStyle().Bold(true)
	sAccent = lipgloss.NewStyle().Foreground(accent)
	sSubtle = lipgloss.NewStyle().Foreground(subtle)
	sFaint  = lipgloss.NewStyle().Foreground(faint)
	sRed    = lipgloss.NewStyle().Foreground(red)
	sGreen  = lipgloss.NewStyle().Foreground(green)
	sYellow = lipgloss.NewStyle().Foreground(yellow)
	sCursor = lipgloss.NewStyle().Background(curBg)
	sBox    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(faint)
)

func (m *model) View() string {
	if m.width == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	list := sBox.Width(m.listWidth()).Height(m.height - 5).Render(m.list())
	diff := sBox.Width(m.diff.Width).Height(m.height - 5).Render(m.diff.View())
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, list, diff) + "\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m *model) header() string {
	r := m.run
	c := r.Findings.Counts()
	h := sBold.Render("lgtm") + " ▸ " + sAccent.Render(r.Branch) + sFaint.Render(" → "+r.Base)
	switch {
	case m.pending != nil:
		h += "   " + sYellow.Render(fmt.Sprintf("%d need you", len(m.open)))
	case r.Phase != "":
		h += "   " + m.spin.View() + " " + sSubtle.Render(string(r.Phase))
	}
	if r.Round > 0 || r.Findings.Closed {
		h += sFaint.Render(fmt.Sprintf("   round %d/%d   %d open · %d fixed · %d filed", r.Round, r.MaxRounds, c.Open, c.Fixed, c.Filed))
	}
	h += sFaint.Render(fmt.Sprintf("   ≈$%.2f", r.CostUSD))
	return lipgloss.NewStyle().MaxWidth(m.width).Render(h)
}

func (m *model) list() string {
	if m.pending == nil {
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
			fmt.Fprintf(&b, " %s %-13s %s\n", glyph, l.Name, sFaint.Render(l.Model))
		}
		if len(m.notes) > 0 {
			b.WriteString("\n")
			for _, n := range m.notes {
				b.WriteString(" " + sSubtle.Render(clip(n, m.listWidth()-3)) + "\n")
			}
		}
		return b.String()
	}
	var b strings.Builder
	for i, f := range m.open {
		glyph := sFaint.Render("·")
		switch m.marks[f.ID] {
		case ceremony.Fix:
			glyph = sAccent.Render("f")
		case ceremony.Accept:
			glyph = sGreen.Render("a")
		case ceremony.Dismiss:
			glyph = sFaint.Render("d")
		case ceremony.Skip:
			if _, ok := m.marks[f.ID]; ok {
				glyph = sSubtle.Render("s")
			}
		}
		sev := sYellow.Render(string(f.Severity))
		if f.Severity == finding.Block {
			sev = sRed.Render(string(f.Severity))
		}
		loc := ""
		if f.Line > 0 {
			loc = fmt.Sprintf(":%d", f.Line)
		}
		line1 := fmt.Sprintf(" %s %-5s %-12s %s", glyph, sev, sSubtle.Render(f.Lens), sFaint.Render(loc))
		line2 := "   " + clip(f.Rule, m.listWidth()-6)
		if i == m.cursor {
			line1 = sCursor.Width(m.listWidth() - 2).Render(line1)
			line2 = sCursor.Width(m.listWidth() - 2).Render(line2)
		}
		b.WriteString(line1 + "\n" + line2 + "\n")
	}
	return b.String()
}

func (m *model) footer() string {
	if m.pending == nil {
		return sSubtle.Render(" q cancel")
	}
	keys := []string{"f", "fix", "a", "accept", "d", "dismiss", "s", "skip", "A", "autopilot", "⏎", "submit", "q", "hold"}
	if !m.canFix {
		keys = keys[2:]
	}
	var b strings.Builder
	for i := 0; i+1 < len(keys); i += 2 {
		if i > 0 {
			b.WriteString(sFaint.Render(" · "))
		}
		b.WriteString(sAccent.Render(keys[i]) + " " + sSubtle.Render(keys[i+1]))
	}
	return " " + b.String()
}

// renderDiff shows the hunk the current finding points at, anchor line marked.
func (m *model) renderDiff() {
	if m.pending == nil || len(m.open) == 0 {
		m.diff.SetContent("")
		return
	}
	f := m.open[m.cursor]
	var b strings.Builder
	b.WriteString(sBold.Render(f.Path))
	if f.Line > 0 {
		b.WriteString(sFaint.Render(fmt.Sprintf(":%d", f.Line)))
	}
	b.WriteString("  " + sFaint.Render(f.Lens+" · "+f.Rule) + "\n\n")
	lines, anchorIdx := hunkAround(m.files, f.Path, f.Line, 10)
	if len(lines) == 0 {
		b.WriteString(sFaint.Render("  (no hunk — a finding about the change as a whole)") + "\n")
	}
	for i, l := range lines {
		num := "     "
		if l.NewNum > 0 {
			num = fmt.Sprintf("%5d", l.NewNum)
		}
		var text string
		switch l.Kind {
		case diffparse.Added:
			text = sGreen.Render("+ " + l.Text)
		case diffparse.Removed:
			text = sRed.Render("- " + l.Text)
		default:
			text = "  " + l.Text
		}
		row := sFaint.Render(num) + " " + text
		if i == anchorIdx {
			row = sYellow.Render("▸") + sCursor.Width(m.diff.Width-1).Render(sFaint.Render(num)+" "+text)
		} else {
			row = " " + row
		}
		b.WriteString(row + "\n")
	}
	b.WriteString("\n" + lipgloss.NewStyle().Width(m.diff.Width-2).Render(f.Body) + "\n")
	m.diff.SetContent(b.String())
	if anchorIdx > m.diff.Height/2 {
		m.diff.SetYOffset(anchorIdx - m.diff.Height/2)
	} else {
		m.diff.GotoTop()
	}
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

// chanDecider blocks the ceremony goroutine until the screen answers.
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

// transcript captures everything the ceremony prints. Lines show in the notes
// pane while the screen is up and are replayed to stdout afterwards.
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
