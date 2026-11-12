// PROTOTYPE — throwaway. Not production code, no tests, do not import.
//
// Question: can the daily review screen reach a btop-grade look, with the
// day strip as the picture and stretches as the unit you select and edit?
//
// One screen over one seeded day of fake events: braille density lanes per
// ticket over a derived-time track, meetings as tinted bands, a stretch list
// that drives the selection, ticket meters and the draft that would be sent.
//
// Run: go run ./cmd/prototype-daystrip   (needs 96×30 or larger, truecolour)
//
// Derivation rules being tried (all in derive):
//   - two events at most idleGap apart join; the time between them is split
//     at the midpoint between their tickets
//   - a longer silence ends the stretch tailCredit minutes after the event
//   - a meeting wins every minute it covers, whatever fired during it
//   - silence of idleGap or more between the day's first and last evidence
//     is a gap
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	dayStart   = 8 * 60 // 08:00
	dayMinutes = 12 * 60
	bucket     = 10
	buckets    = dayMinutes / bucket // 72: fits 80 columns beside an 8-column label
	idleGap    = 10
	tailCredit = 5

	none = -1
	gap  = -2
	meet = 100 // owner meet+i is meeting i
)

type ticket struct{ key, summary string }
type event struct{ min, ticket int }
type meeting struct {
	from, to int
	title    string
	during   int // events that fired inside it
	suggest  int // ticket its title names, or none
}
type block struct{ from, to, owner, gapIdx int }

var tickets = []ticket{
	{"PUN-142", "Wire flock on the side lock"},
	{"OPS-77", "Rotate staging certs"},
	{"PUN-150", "Stop dialog: duration field"},
}

var meetings = []meeting{
	{from: at(10, 0), to: at(10, 45), title: "Sprint planning", suggest: none},
	{from: at(14, 0), to: at(14, 30), title: "PUN-150 swarm", suggest: 2},
}

func at(h, m int) int { return h*60 + m - dayStart }

// --- seeded day ---

var seed uint32 = 7

func rnd(lo, hi int) int {
	seed = seed*1664525 + 1013904223
	return lo + int(seed>>16)%(hi-lo+1)
}

func burst(t, from, to, lo, hi int) (ev []event) {
	for m := from + rnd(0, 2); m < to; m += rnd(lo, hi) {
		ev = append(ev, event{m, t})
	}
	return ev
}

func interleave(a, b, from, to, chunk int) (ev []event) {
	for m, i := from, 0; m < to; m, i = m+chunk, i+1 {
		t := a
		if i%2 == 1 {
			t = b
		}
		ev = append(ev, burst(t, m, min(m+chunk, to), 1, 3)...)
	}
	return ev
}

func seedDay() []event {
	var ev []event
	ev = append(ev, burst(0, at(8, 10), at(9, 20), 1, 4)...)        // heads-down
	ev = append(ev, interleave(0, 1, at(9, 20), at(10, 0), 6)...)   // hopping
	ev = append(ev, event{at(10, 20), 1})                           // a prompt mid-meeting
	ev = append(ev, burst(1, at(10, 50), at(11, 40), 2, 5)...)      //
	ev = append(ev, burst(0, at(12, 30), at(13, 5), 1, 3)...)       // after lunch
	ev = append(ev, burst(0, at(13, 5), at(13, 55), 7, 9)...)       // sparse: reading
	ev = append(ev, interleave(2, 1, at(14, 35), at(15, 30), 9)...) // hopping
	ev = append(ev, burst(2, at(15, 55), at(17, 10), 1, 5)...)      //
	sort.Slice(ev, func(i, j int) bool { return ev[i].min < ev[j].min })
	return ev
}

// --- derivation ---

type day struct {
	events []event
	owner  [dayMinutes]int
	blocks []block
	gaps   []block
	cnt    [][buckets]int // events per ticket per bucket
}

func derive(ev []event) *day {
	d := &day{events: ev, cnt: make([][buckets]int, len(tickets))}
	for i := range d.owner {
		d.owner[i] = none
	}
	fill := func(from, to, o int) {
		for m := max(from, 0); m < min(to, dayMinutes); m++ {
			d.owner[m] = o
		}
	}
	for i, e := range ev {
		d.cnt[e.ticket][e.min/bucket]++
		if i+1 < len(ev) && ev[i+1].min-e.min <= idleGap {
			mid := (e.min + ev[i+1].min + 1) / 2
			fill(e.min, mid, e.ticket)
			fill(mid, ev[i+1].min, ev[i+1].ticket)
			continue
		}
		end := e.min + tailCredit
		if i+1 < len(ev) {
			end = min(end, ev[i+1].min)
		}
		fill(e.min, end, e.ticket)
	}
	for i := range meetings {
		mt := &meetings[i]
		mt.during = 0
		for _, e := range ev {
			if e.min >= mt.from && e.min < mt.to {
				mt.during++
			}
		}
		fill(mt.from, mt.to, meet+i)
	}
	first, last := ev[0].min, ev[len(ev)-1].min
	for m := first; m < last; m++ {
		if d.owner[m] == none {
			d.owner[m] = gap
		}
	}
	for m := 0; m < dayMinutes; {
		o := d.owner[m]
		n := m
		for n < dayMinutes && d.owner[n] == o {
			n++
		}
		if o == gap && n-m < idleGap { // too short to be worth a row
			for x := m; x < n; x++ {
				d.owner[x] = none
			}
			o = none
		}
		if o != none {
			b := block{from: m, to: n, owner: o, gapIdx: none}
			if o == gap {
				b.gapIdx = len(d.gaps)
				d.gaps = append(d.gaps, b)
			}
			d.blocks = append(d.blocks, b)
		}
		m = n
	}
	return d
}

func (d *day) minutesOf(o int) (n int) {
	for _, x := range d.owner {
		if x == o {
			n++
		}
	}
	return n
}

// bucketOwner is whoever owns most of a bucket's minutes.
func (d *day) bucketOwner(b int) (o, mins int) {
	tally := map[int]int{}
	for m := b * bucket; m < (b+1)*bucket; m++ {
		tally[d.owner[m]]++
	}
	o = none
	for k, v := range tally {
		if k != none && (v > mins || (v == mins && k < o)) {
			o, mins = k, v
		}
	}
	return o, mins
}

// --- model ---

type segment struct {
	from, to int
	owners   []int // tickets in a work stretch, or one meeting / gap owner
	gapIdx   int
}

type model struct {
	d        *day
	w, h     int
	events   bool // the audit view is open in place of the lower panels
	cur      int
	adjust   []int // minutes added by hand per ticket
	meetTo   []int // ticket a meeting is logged to, or none
	gapTo    []int // ticket a gap is logged to, or none
	segments []segment
}

func newModel() model { return modelFor(derive(seedDay())) }

func modelFor(d *day) model {
	m := model{d: d, w: 120, h: 36, adjust: make([]int, len(tickets)), meetTo: make([]int, len(meetings)), gapTo: make([]int, len(d.gaps))}
	for i := range m.meetTo {
		m.meetTo[i] = none
	}
	for i := range m.gapTo {
		m.gapTo[i] = none
	}
	for _, b := range d.blocks { // a run of ticket blocks is one stretch
		isWork := b.owner >= 0 && b.owner < meet
		if n := len(m.segments); isWork && n > 0 && m.segments[n-1].to == b.from && isTicket(m.segments[n-1].owners[0]) {
			s := &m.segments[n-1]
			s.to = b.to
			if !contains(s.owners, b.owner) {
				s.owners = append(s.owners, b.owner)
			}
			continue
		}
		m.segments = append(m.segments, segment{b.from, b.to, []int{b.owner}, b.gapIdx})
	}
	return m
}

func isTicket(o int) bool { return o >= 0 && o < meet }

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (m model) ownedIn(t, from, to int) (n int) {
	for mm := from; mm < to; mm++ {
		if m.d.owner[mm] == t {
			n++
		}
	}
	return n
}

func (m model) total(t int) int {
	n := m.d.minutesOf(t) + m.adjust[t]
	for i, to := range m.meetTo {
		if to == t {
			n += meetings[i].to - meetings[i].from
		}
	}
	for i, to := range m.gapTo {
		if to == t {
			n += m.d.gaps[i].to - m.d.gaps[i].from
		}
	}
	return max(n, 0)
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch k := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = k.Width, k.Height
	case tea.KeyPressMsg:
		s := m.segments[m.cur]
		o := s.owners[0]
		cycle := func(cur, suggest int) int {
			if cur == none && suggest != none {
				return suggest
			}
			if cur+1 >= len(tickets) {
				return none
			}
			return cur + 1
		}
		switch k.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "left", "h", "up", "k":
			m.cur = max(m.cur-1, 0)
		case "right", "l", "down", "j":
			m.cur = min(m.cur+1, len(m.segments)-1)
		case "e":
			m.events = !m.events
		case "+", "=":
			if isTicket(o) {
				m.adjust[o] += 5
			}
		case "-":
			if isTicket(o) {
				m.adjust[o] -= 5
			}
		case "t", "enter":
			switch {
			case o >= meet:
				m.meetTo[o-meet] = cycle(m.meetTo[o-meet], meetings[o-meet].suggest)
			case o == gap:
				m.gapTo[s.gapIdx] = cycle(m.gapTo[s.gapIdx], none)
			}
		}
	}
	return m, nil
}

// --- paint ---

type rgb struct{ r, g, b float64 }

func hex(s string) rgb {
	var r, g, b int
	fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b)
	return rgb{float64(r), float64(g), float64(b)}
}

func (c rgb) mix(o rgb, t float64) rgb {
	t = max(0, min(1, t))
	return rgb{c.r + (o.r-c.r)*t, c.g + (o.g-c.g)*t, c.b + (o.b-c.b)*t}
}

func (c rgb) String() string { return fmt.Sprintf("#%02x%02x%02x", int(c.r), int(c.g), int(c.b)) }

var (
	hue    = []rgb{hex("#C8FF00"), hex("#4DD0E1"), hex("#C792EA")}
	amber  = hex("#E6B450")
	text   = hex("#C8C8C8")
	dim    = hex("#7A7A7A")
	faint  = hex("#3C3C3C")
	ink    = hex("#101010")
	selBg  = hex("#2B2B2B")
	meetBg = hex("#2A2212")
	edges  = []rgb{hex("#5F7A2E"), hex("#3F6F78"), hex("#6B5A86"), hex("#7A6A3A")}
)

func fg(c rgb, s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c.String())).Render(s)
}
func bold(c rgb, s string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(c.String())).Render(s)
}
func on(c rgb, bg *rgb, s string) string {
	st := lipgloss.NewStyle().Foreground(lipgloss.Color(c.String()))
	if bg != nil {
		st = st.Background(lipgloss.Color(bg.String()))
	}
	return st.Render(s)
}

func colorOf(o int) rgb {
	switch {
	case o >= meet:
		return amber
	case o >= 0:
		return hue[o]
	}
	return dim
}

func dur(n int) string {
	if n < 60 {
		return fmt.Sprintf("%dm", n)
	}
	return fmt.Sprintf("%dh %02dm", n/60, n%60)
}

func clock(m int) string { return fmt.Sprintf("%02d:%02d", (m+dayStart)/60, (m+dayStart)%60) }

func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
}

// box draws a rounded box with btop-style titles set into the top border.
func box(n int, title, right string, w, h int, body []string) []string {
	e := edges[n-1]
	t := fg(e, "┐") + bold(hue[0], string([]rune("¹²³⁴")[n-1])) + bold(text, title) + fg(e, "┌")
	r := ""
	if right != "" {
		r = fg(e, "┐") + right + fg(e, "┌")
	}
	fill := w - 4 - lipgloss.Width(t) - lipgloss.Width(r)
	out := []string{fg(e, "╭─") + t + fg(e, strings.Repeat("─", max(fill, 0))) + r + fg(e, "─╮")}
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		out = append(out, fg(e, "│")+pad(line, w-2)+fg(e, "│"))
	}
	return append(out, fg(e, "╰"+strings.Repeat("─", w-2)+"╯"))
}

// meter is a btop bar: filled cells run along a gradient, the rest are faint.
func meter(frac float64, w int, c rgb) string {
	var b strings.Builder
	n := int(frac*float64(w) + 0.5)
	for i := 0; i < w; i++ {
		if i < n {
			b.WriteString(fg(c.mix(ink, 0.55).mix(c, float64(i)/float64(max(w-1, 1))*1.6), "■"))
		} else {
			b.WriteString(fg(faint, "■"))
		}
	}
	return b.String()
}

// --- ¹ day: braille lanes over derived tracks ---

const labelW = 10

func (m model) dayBox(w, rows int) []string {
	d := m.d
	g := w - 2 - labelW - 1 // graph columns
	span := func(c int) (int, int) { return c * dayMinutes / g, (c + 1) * dayMinutes / g }
	sel := m.segments[m.cur]
	bgAt := func(c int) *rgb {
		a, b := span(c)
		mid := (a + b) / 2
		switch {
		case mid >= sel.from && mid < sel.to:
			return &selBg
		case d.owner[mid] >= meet:
			return &meetBg
		}
		return nil
	}

	// smoothed event density per ticket, two samples per column
	n := 2 * g
	val := make([][]float64, len(tickets))
	peak := 1.0
	for t := range tickets {
		val[t] = make([]float64, n)
		for s := 0; s < n; s++ {
			at := float64(s) * dayMinutes / float64(n)
			for _, e := range d.events {
				if dist := abs(float64(e.min) - at); e.ticket == t && dist < 6 {
					val[t][s] += 1 - dist/6
				}
			}
			peak = max(peak, val[t][s])
		}
	}

	var out []string
	axis := []rune(strings.Repeat(" ", g))
	for hr := 0; hr < dayMinutes/60; hr++ {
		for i, r := range fmt.Sprintf("%02d", hr+dayStart/60) {
			axis[hr*60*g/dayMinutes+i] = r
		}
	}
	out = append(out, strings.Repeat(" ", labelW)+fg(dim, string(axis)))

	left := []rune{0x40, 0x04, 0x02, 0x01} // braille dots, bottom to top
	rightDots := []rune{0x80, 0x20, 0x10, 0x08}
	for t, tk := range tickets {
		for r := 0; r < rows; r++ {
			label := strings.Repeat(" ", labelW)
			switch r {
			case 0:
				label = pad(" "+bold(hue[t], tk.key), labelW)
			}
			var b strings.Builder
			for c := 0; c < g; c++ {
				ch := rune(0x2800)
				top := 0.0
				for side, dots := range [][]rune{left, rightDots} {
					v := val[t][2*c+side] / peak
					top = max(top, v)
					lvl := int(v*float64(4*rows) + 0.99)
					for i := 0; i < min(max(lvl-(rows-1-r)*4, 0), 4); i++ {
						ch |= dots[i]
					}
				}
				if ch == 0x2800 {
					ch = ' '
				}
				b.WriteString(on(hue[t].mix(ink, 0.6).mix(hue[t], top*1.5), bgAt(c), string(ch)))
			}
			out = append(out, label+b.String())
		}
		// counted time: a solid bar wherever this ticket is credited, with
		// its total beside it so the number and the bar read as one thing
		var b strings.Builder
		for c := 0; c < g; c++ {
			a, z := span(c)
			own := 0
			for mm := a; mm < z; mm++ {
				if m.eff(mm) == t {
					own++
				}
			}
			if own*2 >= z-a {
				b.WriteString(on(hue[t], bgAt(c), "▀"))
			} else {
				b.WriteString(on(faint, bgAt(c), " "))
			}
		}
		out = append(out, pad(" "+fg(text, dur(m.total(t))), labelW)+b.String())
	}

	// unlogged: meetings and silences nobody has assigned
	var b strings.Builder
	for c := 0; c < g; c++ {
		a, z := span(c)
		switch e := m.eff((a + z) / 2); {
		case e >= meet:
			b.WriteString(on(amber, bgAt(c), "▀"))
		case e == gap:
			b.WriteString(on(dim, bgAt(c), "░"))
		default:
			b.WriteString(on(faint, bgAt(c), " "))
		}
	}
	return append(out, pad(" "+fg(dim, "unlogged"), labelW)+b.String())
}

// eff is who a minute is credited to once meetings and gaps are assigned.
func (m model) eff(mm int) int {
	o := m.d.owner[mm]
	switch {
	case o >= meet && m.meetTo[o-meet] != none:
		return m.meetTo[o-meet]
	case o == gap:
		for i, gp := range m.d.gaps {
			if mm >= gp.from && mm < gp.to && m.gapTo[i] != none {
				return m.gapTo[i]
			}
		}
	}
	return o
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// --- ² stretches: the selector ---

func (m model) assigned(to int) string {
	if to == none {
		return fg(dim, "not logged")
	}
	return fg(hue[to], "→ "+tickets[to].key)
}

func (m model) stretchBox(w, h int) []string {
	// A matrix: one row per stretch, one column per ticket, so a stretch
	// shared by several tickets never needs a label that can be cut, and
	// each column adds up to its ticket's total. Numbers sit at the right
	// edge; what a meeting or gap is takes whatever width is left, and the
	// selected row is spelled out in full on the last line.
	const cw = 9
	iw := w - 2
	whatW := iw - 1 - 11 - 3 - cw*(len(tickets)+1) - 1
	what := func(s string) string {
		if whatW < 6 {
			return ""
		}
		return pad(s, whatW)
	}
	cell := func(n int, c rgb) string {
		if n == 0 {
			return fg(faint, fmt.Sprintf("%*s", cw, "·"))
		}
		return fg(c, fmt.Sprintf("%*s", cw, dur(n)))
	}
	head := " " + fg(dim, fmt.Sprintf("%-11s", "stretch")) + "   " + what("")
	for t, tk := range tickets {
		head += bold(hue[t], fmt.Sprintf("%*s", cw, tk.key))
	}
	head += fg(dim, fmt.Sprintf("%*s", cw, "unlogged"))
	body := []string{head}
	var rows []string
	offSum, detail := 0, ""
	for i, s := range m.segments {
		per := make([]int, len(tickets))
		off, mark, name, more := 0, " ", "", ""
		n := 0
		for _, e := range m.d.events {
			if e.min >= s.from && e.min < s.to {
				n++
			}
		}
		switch o := s.owners[0]; {
		case o >= meet:
			mt := meetings[o-meet]
			mark, name = fg(amber, "▀"), fg(amber, mt.title)
			more = fg(amber, mt.title) + fg(dim, " · meeting, ") + m.assigned(m.meetTo[o-meet])
			if to := m.meetTo[o-meet]; to != none {
				per[to] = s.to - s.from
			} else {
				off = s.to - s.from
				if mt.suggest != none {
					more += fg(dim, " · its title names "+tickets[mt.suggest].key)
				}
			}
			if mt.during > 0 {
				more += fg(dim, fmt.Sprintf(" · %d event fired during it", mt.during))
			}
		case o == gap:
			mark, name = fg(dim, "░"), fg(dim, "no evidence")
			more = fg(dim, "no evidence for "+dur(s.to-s.from)+", ") + m.assigned(m.gapTo[s.gapIdx])
			if to := m.gapTo[s.gapIdx]; to != none {
				per[to] = s.to - s.from
			} else {
				off = s.to - s.from
			}
		default:
			var parts []string
			for _, t := range s.owners {
				per[t] = m.ownedIn(t, s.from, s.to)
				parts = append(parts, fg(hue[t], tickets[t].key)+" "+fg(text, dur(per[t])))
			}
			more = strings.Join(parts, fg(dim, " · ")) + fg(dim, fmt.Sprintf(" · from %d events", n))
		}
		offSum += off
		row := " " + fg(text, clock(s.from)+"–"+clock(s.to)) + " " + mark + " " + what(name)
		for t := range tickets {
			row += cell(per[t], hue[t])
		}
		row += cell(off, dim)
		if i == m.cur {
			row = lipgloss.NewStyle().Background(lipgloss.Color(selBg.String())).Render(pad(bold(hue[0], "›")+row[1:], iw))
			detail = more
		}
		rows = append(rows, row)
	}
	room := h - 2 - 5 // header, rule, total, blank, detail
	if first := m.cur - room + 1; first > 0 {
		rows = rows[first:]
	}
	if len(rows) > room {
		rows = rows[:room]
	}
	body = append(body, rows...)
	foot := " " + fg(dim, fmt.Sprintf("%-11s", "total")) + "   " + what("")
	for t := range tickets {
		foot += bold(hue[t], fmt.Sprintf("%*s", cw, dur(m.total(t))))
	}
	foot += fg(dim, fmt.Sprintf("%*s", cw, dur(offSum)))
	body = append(body, " "+fg(faint, strings.Repeat("─", iw-2)), foot)
	for len(body) < h-3 {
		body = append(body, "")
	}
	body = append(body, " "+bold(hue[0], "› ")+detail)
	return box(2, "stretches", fg(dim, fmt.Sprintf("%d/%d", m.cur+1, len(m.segments))), w, h, body)
}

// --- events: the audit view ---

var branch = []string{"feature/PUN-142-side-lock", "ops/OPS-77-staging-certs", "feature/PUN-150-stop-duration"}

// kind fakes where an event came from: mostly prompts, the odd commit.
func kind(e event) (glyph, name string) {
	if e.min%11 == 0 {
		return "◆", "commit"
	}
	return "│", "prompt"
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// eventBox audits the selected stretch in the main screen's own grammar:
// per ticket a row of holes (one per event) over the bar of minutes counted
// for it; then where the events came from, once; then every event with the
// silence before it, which is the number the idle-gap rule acts on.
func (m model) eventBox(w, h int) []string {
	s := m.segments[m.cur]
	iw := w - 2
	g := iw - labelW - 1
	col := func(mm int) int { return min((mm-s.from)*g/(s.to-s.from), g-1) }
	mid := func(c int) int { return s.from + (2*c+1)*(s.to-s.from)/(2*g) }
	var evs []event
	for _, e := range m.d.events {
		if e.min >= s.from && e.min < s.to {
			evs = append(evs, e)
		}
	}

	axis := []rune(strings.Repeat(" ", g))
	for mm := (s.from + 9) / 10 * 10; mm < s.to; mm += 10 {
		if c := col(mm); c+5 <= g && (c == 0 || axis[c-1] == ' ') {
			copy(axis[c:], []rune(clock(mm)))
		}
	}
	body := []string{strings.Repeat(" ", labelW) + fg(dim, string(axis))}
	var sessions []string
	for t, tk := range tickets {
		holes := make([]string, g)
		prompts, commits := 0, 0
		for _, e := range evs {
			if e.ticket == t {
				c := col(e.min)
				switch _, name := kind(e); {
				case name == "commit":
					holes[c] = "◆"
					commits++
				case holes[c] == "":
					holes[c] = "│"
					prompts++
				default:
					if holes[c] == "│" {
						holes[c] = "┃"
					}
					prompts++
				}
			}
		}
		counted := 0
		for mm := s.from; mm < s.to; mm++ {
			if m.eff(mm) == t {
				counted++
			}
		}
		if prompts+commits == 0 && counted == 0 {
			continue
		}
		var hb, cb strings.Builder
		for c, hl := range holes {
			switch hl {
			case "":
				hb.WriteString(fg(faint, "·"))
			case "◆":
				hb.WriteString(bold(hex("#FFFFFF"), hl))
			default:
				hb.WriteString(fg(hue[t], hl))
			}
			if m.eff(mid(c)) == t {
				cb.WriteString(fg(hue[t].mix(ink, 0.5), "▀"))
			} else {
				cb.WriteString(" ")
			}
		}
		label := " " + fg(text, dur(counted))
		if counted == 0 {
			label = ""
		}
		body = append(body, pad(" "+bold(hue[t], tk.key), labelW)+hb.String(), pad(label, labelW)+cb.String())
		if prompts+commits > 0 {
			n := plural(prompts, "prompt")
			if commits > 0 {
				n += " · " + plural(commits, "commit")
			}
			// show the key where it was found, so the attribution explains itself
			pre, post, _ := strings.Cut(branch[t], tk.key)
			sessions = append(sessions, " "+pad(bold(hue[t], tk.key), 9)+fg(dim, "← branch ")+fg(dim, pre)+bold(hue[t], tk.key)+fg(dim, post)+fg(dim, " · claude session · "+n))
		}
	}
	var ub strings.Builder
	unlogged := 0
	for c := 0; c < g; c++ {
		switch o := m.eff(mid(c)); {
		case o >= meet:
			ub.WriteString(fg(amber, "▀"))
			unlogged++
		case o == gap:
			ub.WriteString(fg(dim, "░"))
			unlogged++
		default:
			ub.WriteString(" ")
		}
	}
	if unlogged > 0 {
		body = append(body, pad(" "+fg(dim, "unlogged"), labelW)+ub.String())
	}
	body = append(body, "")
	if len(sessions) > 0 {
		body = append(body, sessions...)
		body = append(body, "")
	}

	// every event, reading across: time, kind, and the silence before it
	const cellW = 15
	per := max((iw-2)/cellW, 1)
	room := (h - 2 - len(body)) * per
	var line strings.Builder
	for i, e := range evs {
		if i == room-1 && len(evs) > room {
			line.WriteString(fg(dim, fmt.Sprintf("+ %d more", len(evs)-i)))
			break
		}
		gl, name := kind(e)
		gc := hue[e.ticket]
		if name == "commit" {
			gc = hex("#FFFFFF")
		}
		since := ""
		if i > 0 {
			d := e.min - evs[i-1].min
			c := faint
			if d >= idleGap-2 { // close to the idle gap: one slower prompt would have split this
				c = amber
			}
			since = fg(c, fmt.Sprintf("+%dm", d))
		}
		line.WriteString(pad(fg(text, clock(e.min))+" "+bold(gc, gl)+" "+since, cellW))
		if (i+1)%per == 0 {
			body = append(body, " "+line.String())
			line.Reset()
		}
	}
	if line.Len() > 0 {
		body = append(body, " "+line.String())
	}
	if len(evs) == 0 {
		body = append(body, " "+fg(dim, "nothing fired in this stretch"))
	}
	legend := fg(text, "│") + fg(dim, " prompt  ") + fg(text, "┃") + fg(dim, " several  ") + bold(hex("#FFFFFF"), "◆") + fg(dim, " commit  ") + fg(hue[0].mix(ink, 0.5), "▀") + fg(dim, " counted  ") + fg(amber, "+9m") + fg(dim, " near the idle gap")
	return box(2, fmt.Sprintf("events · %s–%s · %d", clock(s.from), clock(s.to), len(evs)), legend, w, h, body)
}

// --- ³ draft: the worklogs that would be sent ---

func (m model) draftBox(w, h int) []string {
	iw := w - 2
	sent, meetOff, gapOff := 0, 0, 0
	for t := range tickets {
		sent += m.total(t)
	}
	for i, mt := range meetings {
		if m.meetTo[i] == none {
			meetOff += mt.to - mt.from
		}
	}
	for i, gp := range m.d.gaps {
		if m.gapTo[i] == none {
			gapOff += gp.to - gp.from
		}
	}
	// every number ends in the same column
	line := func(left, val string) string {
		return " " + pad(left, iw-2-8) + val + " "
	}
	num := func(c rgb, n int) string { return bold(c, fmt.Sprintf("%8s", dur(n))) }
	body := []string{""}
	for t, tk := range tickets {
		mark := ""
		if m.adjust[t] != 0 {
			mark = fg(amber, fmt.Sprintf("  %+dm by hand", m.adjust[t]))
		}
		body = append(body,
			line(bold(hue[t], fmt.Sprintf("%-8s", tk.key))+" "+fg(dim, tk.summary)+mark, ""),
			line(meter(float64(m.total(t))/float64(max(sent, 1)), iw-2-8-1, hue[t]), num(text, m.total(t))),
			"")
	}
	body = append(body,
		" "+fg(faint, strings.Repeat("─", iw-2)),
		line(fg(text, "to send")+fg(dim, fmt.Sprintf(" · %d worklogs → Jira", len(tickets))), num(hue[0], sent)),
		line(fg(text, "unlogged")+fg(dim, " · meetings "+dur(meetOff)+" · gaps "+dur(gapOff)), num(dim, meetOff+gapOff)))
	return box(3, "draft", bold(hue[0], "⏎")+fg(text, " confirm & send"), w, h, body)
}

func (m model) View() tea.View {
	var out []string
	if m.w < 96 || m.h < 30 {
		out = []string{fmt.Sprintf("PROTOTYPE: needs 96×30 or larger (now %d×%d)", m.w, m.h)}
	} else {
		botH := max(len(m.segments)+7, len(tickets)*3+6) // sized to its rows; the lanes get the rest
		rows := max(2, min(6, (m.h-1-botH-4-len(tickets))/len(tickets)))
		topH := 1 + len(tickets)*(rows+1) + 1 + 2
		legend := fg(hue[0], "▀") + fg(dim, " counted  ") + fg(amber, "▀") + fg(dim, " meeting  ░ no evidence")
		out = box(1, "day · Tue 10 Nov", legend, m.w, topH, m.dayBox(m.w, rows))
		botH = m.h - topH - 1
		lw := m.w * 58 / 100
		rw := m.w - lw
		if m.events {
			out = append(out, m.eventBox(m.w, botH)...)
		} else {
			l := m.stretchBox(lw, botH)
			r := m.draftBox(rw, botH)
			for i := range l {
				out = append(out, l[i]+r[i])
			}
		}
		key := func(k, what string) string { return bold(hue[0], k) + fg(dim, " "+what+"   ") }
		out = append(out, " "+key("↑↓", "stretch")+key("t", "assign meeting or gap")+key("+ −", "adjust 5m")+key("e", "events")+key("q", "quit")+fg(faint, "PROTOTYPE · fake data"))
	}
	v := tea.NewView(strings.Join(out, "\n"))
	v.AltScreen = true
	return v
}

func main() {
	if _, err := tea.NewProgram(newModel()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
