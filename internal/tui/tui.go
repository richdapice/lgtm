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
	h := sBold.Render("lgtm") + sFaint.Render(" · ") + sAccent.Render(r.Branch) + sFaint.Render(" → "+r.Base)
	switch {
	case m.pending != nil:
		n := len(m.open)
		word := "findings need"
		if n == 1 {
			word = "finding needs"
		}
		h += "      " + sYellow.Render(fmt.Sprintf("%d %s a decision", n, word))
	case r.Phase != "":
		h += "      " + sAccent.Render(m.spin.View()) + " " + sSubtle.Render(string(r.Phase))
		if r.Round > 0 {
			h += sFaint.Render(fmt.Sprintf(" · round %d of %d", r.Round, r.MaxRounds))
		}
	}
	h += sFaint.Render(fmt.Sprintf("      ≈$%.2f", r.CostUSD))
	return h
}

// progress is the live view between decisions: the gates, where the run is,
// and the rounds it has been through. This is what the run looks like from
// the outside.
func (m *model) progress() string {
	var b strings.Builder
	r := m.run
	b.WriteString("\n " + sSubtle.Render("GATES") + "\n")
	reached := false
	for _, g := range run.Gates {
		mark, name, note := sFaint.Render("○"), sFaint.Render(pad(g, 8)), ""
		switch {
		case g == r.Step && !reached:
			reached = true
			mark, name = sAccent.Render(m.spin.View()), sAccent.Render(pad(g, 8))
			note = sSubtle.Render(r.StepNote)
		case !reached:
			mark, name = sGreen.Render("✓"), pad(g, 8)
			note = sFaint.Render(gateSummary(g, r))
		}
		fmt.Fprintf(&b, "   %s %s  %s\n", mark, name, note)
	}
	if r.MaxRounds > 0 && (r.Round > 0 || len(r.Rounds) > 0) {
		b.WriteString("\n " + sSubtle.Render("ROUNDS") + "  ")
		for i := 0; i < r.MaxRounds; i++ {
			if i < len(r.Rounds) {
				b.WriteString(sGreen.Render("● "))
			} else if i == len(r.Rounds) && r.Round > len(r.Rounds) {
				b.WriteString(sAccent.Render("◐ "))
			} else {
				b.WriteString(sFaint.Render("○ "))
			}
		}
		for i, rs := range r.Rounds {
			if rs.Reverted {
				fmt.Fprintf(&b, "   %s", sFaint.Render(fmt.Sprintf("round %d: fix reverted, checks failed", i+1)))
			} else {
				fmt.Fprintf(&b, "   %s", sFaint.Render(fmt.Sprintf("round %d: %d fixed · %d filed · %d open", i+1, rs.Fixed, rs.Filed, rs.Open)))
			}
		}
		b.WriteString("\n")
	}
	for _, n := range m.notes {
		b.WriteString("   " + sSubtle.Render(clip(n, m.width-4)) + "\n")
	}
	b.WriteString("\n " + sFaint.Render("q cancel") + "\n")
	return b.String()
}

// gateSummary is the one-line result of a gate already passed.
func gateSummary(g string, r run.Run) string {
	c := r.Findings.Counts()
	switch g {
	case "review":
		n := len(r.Lenses)
		if n == 1 {
			n = 4
		}
		return fmt.Sprintf("%d lenses · %d found", n, r.Findings.Discovered())
	case "decide":
		return fmt.Sprintf("%d fixed · %d accepted · %d dismissed", c.Fixed, c.Accepted, c.Dismissed)
	case "fix", "check", "verify":
		if len(r.Rounds) > 0 {
			last := r.Rounds[len(r.Rounds)-1]
			return fmt.Sprintf("round %d", len(r.Rounds)) + map[bool]string{true: " · reverted", false: fmt.Sprintf(" · %d confirmed", last.Fixed)}[last.Reverted]
		}
	case "push":
		return "origin/" + r.Branch
	case "pr":
		if r.PR != nil {
			return fmt.Sprintf("#%d", r.PR.Number)
		}
	case "ci":
		if r.CI != nil {
			return fmt.Sprintf("%d/%d green", r.CI.Passed, r.CI.Total)
		}
	}
	return ""
}

func pad(s string, w int) string {
	for len([]rune(s)) < w {
		s += " "
	}
	return s
}

func decisionWord(d ceremony.Decision, decided bool) string {
	if !decided {
		return "undecided"
	}
	switch d {
	case ceremony.Fix:
		return "fix"
	case ceremony.Accept:
		return "accept"
	case ceremony.Dismiss:
		return "dismiss"
	}
	return "skip"
}

// review is laid out for someone seeing it the first time: numbered
// findings with their decision spelled out, one section per thing, one
// marker (▶) that always means "this is where you are", and a question
// above the actions so the buttons have a subject.
func (m *model) review() string {
	var b strings.Builder
	w := m.width
	cur := m.open[m.cursor]

	b.WriteString("\n " + sSubtle.Render("FINDINGS") + "\n")
	for i, f := range m.open {
		d, decided := m.marks[f.ID]
		word := decisionWord(d, decided)
		if decided {
			word = sGreen.Render(word)
		} else {
			word = sFaint.Render(word)
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
			rule += sYellow.Render("  (needs your call)")
		}
		mark := " "
		if i == m.cursor {
			mark = sAccent.Render("▶")
		}
		line := fmt.Sprintf(" %s %d  %s  %-28s %s", mark, i+1, sev, loc, rule)
		// the decision column sits at the right edge, inside the highlight
		pad := w - 2 - lipgloss.Width(line) - lipgloss.Width(word) - 4
		if pad < 2 {
			pad = 2
		}
		line += strings.Repeat(" ", pad) + "[ " + word + " ]"
		if i == m.cursor {
			line = sRow.Render(line)
		}
		b.WriteString(line + "\n")
	}

	b.WriteString("\n " + sSubtle.Render(fmt.Sprintf("FINDING %d", m.cursor+1)) +
		sFaint.Render(" · "+cur.Lens+" · "+cur.Path) + "\n")
	lines, anchor := hunkAround(m.files, cur.Path, cur.Line, 4)
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
			mark = sAccent.Render("▶")
		}
		fmt.Fprintf(&b, " %s %s %s\n", mark, sFaint.Render(num), text)
	}
	b.WriteString(indent(lipgloss.NewStyle().Width(w-4).Render(cur.Body), "   ") + "\n")
	if cur.Note != "" {
		b.WriteString(indent(sYellow.Render(lipgloss.NewStyle().Width(w-6).Render(cur.Note)), "   ↳ ") + "\n")
	}

	b.WriteString("\n")
	if m.allMarked() {
		b.WriteString(" " + sSubtle.Render("EVERY FINDING HAS A DECISION") + "\n")
		b.WriteString("   " + sSel.Render("Enter: submit") + "   " + sFaint.Render("↑↓ go back and change one · q quit for now") + "\n")
		return b.String()
	}
	b.WriteString(" " + sSubtle.Render(fmt.Sprintf("WHAT DO YOU WANT TO DO WITH FINDING %d?", m.cursor+1)) + "\n   ")
	for i, a := range actions {
		switch {
		case i == m.action:
			b.WriteString(sSel.Render("▶ " + a.label))
		case i == 0 && !m.canFix:
			b.WriteString(sOptOff.Render("  " + a.label))
		default:
			b.WriteString(sOpt.Render("  " + a.label))
		}
		b.WriteString("  ")
	}
	b.WriteString("\n   " + sFaint.Render("↑↓ pick a finding · ←→ pick an action · Enter to apply · A autopilot · q quit for now") + "\n")
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
