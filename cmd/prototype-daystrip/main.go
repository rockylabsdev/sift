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
	events   int // 0 closed; 1 the selected stretch in context; 2 also every event
	theme    int
	cur      int
	adjust   []int // minutes added by hand per ticket
	meetTo   []int // ticket a meeting is logged to, or none
	gapTo    []int // ticket a gap is logged to, or none
	segments []segment
}

func newModel() model {
	m := modelFor(derive(seedDay()))
	for i, th := range themes {
		if strings.EqualFold(os.Getenv("SIFT_THEME"), th.name) {
			m.theme = i
		}
	}
	return m
}

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

// counted is the minutes the day strip credits to ticket t: one owner per
// minute, so overlapping meetings can never count twice.
func (m model) counted(t int) int { return m.countedAll()[t] }

// countedAll is counted for every ticket in one pass over the day.
func (m model) countedAll() []int {
	n := make([]int, len(tickets))
	for mm := range dayMinutes {
		if o := m.eff(mm); isTicket(o) {
			n[o]++
		}
	}
	return n
}

func (m model) total(t int) int { return m.totals()[t] }

// totals is every ticket's draft total: counted plus adjustment, floored.
func (m model) totals() []int {
	n := m.countedAll()
	for t := range n {
		n[t] = max(n[t]+m.adjust[t], 0)
	}
	return n
}

// byHand is what each hand adjustment actually changed, after the floor.
func (m model) byHand() []int {
	n := m.countedAll()
	for t := range n {
		n[t] = max(n[t]+m.adjust[t], 0) - n[t]
	}
	return n
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
			m.events = (m.events + 1) % 3
		case "esc":
			m.events = 0
		case "T":
			m.theme = (m.theme + 1) % len(themes)
		case "+", "=":
			if isTicket(o) {
				m.adjust[o] += 5
			}
		case "-":
			if isTicket(o) {
				m.adjust[o] = max(m.adjust[o]-5, -m.counted(o))
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

// A theme gives every colour one job. Ticket hues belong to ticket labels and
// their marks; amber marks meetings and things needing attention, always next
// to words; borders are neutral and only the focused panel's is emphasised.
type theme struct {
	name                 string
	bg, text, muted, dim rgb
	faint, border, focus rgb
	sel, amber           rgb
	hue                  []rgb
}

var themes = []theme{
	{"graphite", hex("#181C1B"), hex("#E4DFD3"), hex("#B4B0A5"), hex("#878B84"),
		hex("#343A38"), hex("#3A413E"), hex("#8C978E"), hex("#232B28"), hex("#D4B078"),
		[]rgb{hex("#B5C99B"), hex("#8FBDC1"), hex("#B5A5CC")}},
	{"dusk", hex("#1B1C25"), hex("#E1E1EA"), hex("#B0B1C2"), hex("#878999"),
		hex("#33354A"), hex("#383A4C"), hex("#868AA2"), hex("#272939"), hex("#D8B682"),
		[]rgb{hex("#AABCE0"), hex("#91BEB3"), hex("#C3A6C9")}},
	{"paper", hex("#F2EFE7"), hex("#2B2A26"), hex("#55534C"), hex("#77746A"),
		hex("#D6D2C6"), hex("#C9C5B8"), hex("#8C887B"), hex("#E3E5D8"), hex("#866119"),
		[]rgb{hex("#526D3F"), hex("#306C74"), hex("#765A89")}},
}

var p = &themes[0]

func style(c rgb) lipgloss.Style  { return lipgloss.NewStyle().Foreground(lipgloss.Color(c.String())) }
func fg(c rgb, s string) string   { return style(c).Render(s) }
func bold(c rgb, s string) string { return style(c).Bold(true).Render(s) }
func on(c rgb, bg *rgb, s string) string {
	st := style(c)
	if bg != nil {
		st = st.Background(lipgloss.Color(bg.String()))
	}
	return st.Render(s)
}

// tone is the colour an owner's marks take: its ticket's hue, amber for a
// meeting nobody has assigned, dim for silence.
func tone(o int) rgb {
	switch {
	case isTicket(o):
		return p.hue[o]
	case o >= meet:
		return p.amber
	}
	return p.dim
}

func dur(n int) string {
	if n < 60 {
		return fmt.Sprintf("%dm", n)
	}
	return fmt.Sprintf("%dh %02dm", n/60, n%60)
}

func signed(n int) string {
	if n < 0 {
		return "-" + dur(-n)
	}
	return "+" + dur(n)
}

func clock(m int) string { return fmt.Sprintf("%02d:%02d", (m+dayStart)/60, (m+dayStart)%60) }

func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
}

func spread(left, right string, w int) string {
	return left + strings.Repeat(" ", max(w-lipgloss.Width(left)-lipgloss.Width(right), 1)) + right
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// box draws a panel with its number and title set into the top border.
func box(n int, title, right string, focused bool, w, h int, body []string) []string {
	e := p.border
	if focused {
		e = p.focus
	}
	t := " " + fg(p.dim, fmt.Sprint(n)) + " " + bold(p.text, title) + " "
	r := ""
	if right != "" {
		r = " " + right + " "
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

func (m model) unassigned() (meetOff, gapOff int) {
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
	return meetOff, gapOff
}

func (m model) toSend() (n int) {
	for t := range tickets {
		n += m.total(t)
	}
	return n
}

// --- 1 day: event density over counted time ---

const labelW = 10

// dayHeight is the day box's height for evRows rows of density per lane.
func dayHeight(evRows int, sep bool) int {
	h := 2 + 1 + len(tickets)*(evRows+1) + 3 // borders, axis, lanes, other, selection, summary
	if sep {
		h += len(tickets) - 1
	}
	return h
}

func (m model) dayBox(w, evRows int, sep bool) []string {
	d := m.d
	g := w - 2 - labelW - 1
	span := func(c int) (int, int) { return c * dayMinutes / g, (c + 1) * dayMinutes / g }
	sel := m.segments[m.cur]
	inSel := func(c int) bool {
		a, b := span(c)
		return (a+b)/2 >= sel.from && (a+b)/2 < sel.to
	}
	bgAt := func(c int) *rgb {
		if inSel(c) {
			return &p.sel
		}
		return nil
	}

	// event density, two samples per column, on one scale shared by every
	// lane so a taller lane means more events, never more credit
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

	axis := []rune(strings.Repeat(" ", g))
	for hr := 0; hr < dayMinutes/60; hr += 2 {
		for i, r := range fmt.Sprintf("%02d", hr+dayStart/60) {
			axis[hr*60*g/dayMinutes+i] = r
		}
	}
	out := []string{strings.Repeat(" ", labelW) + fg(p.dim, string(axis))}

	left := []rune{0x40, 0x04, 0x02, 0x01} // braille dots, bottom to top
	rightDots := []rune{0x80, 0x20, 0x10, 0x08}
	for t, tk := range tickets {
		if t > 0 && sep {
			out = append(out, "")
		}
		for r := 0; r < evRows; r++ {
			label := ""
			switch r {
			case 0:
				label = " " + bold(p.hue[t], tk.key)
			case 1:
				label = " " + fg(p.text, dur(m.total(t)))
			}
			var b strings.Builder
			for c := 0; c < g; c++ {
				ch := rune(0x2800)
				for side, dots := range [][]rune{left, rightDots} {
					lvl := int(val[t][2*c+side]/peak*float64(4*evRows) + 0.99)
					for i := 0; i < min(max(lvl-(evRows-1-r)*4, 0), 4); i++ {
						ch |= dots[i]
					}
				}
				if ch == 0x2800 {
					ch = ' '
				}
				b.WriteString(on(p.hue[t], bgAt(c), string(ch)))
			}
			out = append(out, pad(label, labelW)+b.String())
		}
		// counted time is the solid baseline, whatever the ticket's hue
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
				b.WriteString(on(p.hue[t], bgAt(c), "━"))
			} else {
				b.WriteString(on(p.faint, bgAt(c), "─"))
			}
		}
		out = append(out, strings.Repeat(" ", labelW)+b.String())
	}

	// meetings and gaps get their own row: a patterned band with its name,
	// in amber until it is assigned, then in the ticket's hue
	other := make([]string, g)
	for c := range other {
		a, z := span(c)
		switch o, e := d.owner[(a+z)/2], m.eff((a+z)/2); {
		case o >= meet:
			other[c] = on(tone(e), bgAt(c), "▰")
		case o == gap:
			other[c] = on(tone(e), bgAt(c), "░")
		default:
			other[c] = on(p.faint, bgAt(c), " ")
		}
	}
	for i, mt := range meetings {
		c0 := mt.from*g/dayMinutes + 1
		room := mt.to*g/dayMinutes - c0 - 1
		name := []rune(mt.title)
		if room < 3 {
			continue
		}
		if len(name) > room {
			name = append(name[:room-1], '…')
		}
		c := tone(m.eff(mt.from))
		if m.meetTo[i] == none {
			c = p.amber
		}
		for j, r := range name {
			other[c0+j] = on(c, bgAt(c0+j), string(r))
		}
	}
	out = append(out, pad(" "+fg(p.dim, "other"), labelW)+strings.Join(other, ""))

	// the selection: a quiet band above, and its edges marked here
	a := sel.from * g / dayMinutes
	z := max((sel.to*g+dayMinutes-1)/dayMinutes-1, a)
	bracket := "↑"
	if z > a {
		bracket = "└" + strings.Repeat("─", z-a-1) + "┘"
	}
	when := clock(sel.from) + "–" + clock(sel.to)
	mark := strings.Repeat(" ", a) + bracket + " " + when
	if z+2+lipgloss.Width(when) > g {
		mark = strings.Repeat(" ", max(a-lipgloss.Width(when)-1, 0)) + when + " " + bracket
	}
	out = append(out, strings.Repeat(" ", labelW)+fg(p.focus, mark))

	meetOff, gapOff := m.unassigned()
	sum := " " + fg(p.muted, "unassigned") + "  " + fg(p.amber, "▰ meetings "+dur(meetOff)) + fg(p.dim, "  ·  ") + fg(p.muted, "░ gaps "+dur(gapOff))
	note := fg(p.dim, "⣿ event density, one scale  ") + fg(p.muted, "━ counted ")
	return append(out, spread(sum, note, w-2))
}

// --- 2 stretches: the selector ---

// stretchCols lays out the table for an inner width: the name column takes
// whatever is left after the times and one column per ticket plus unassigned.
func stretchCols(iw int) (whatW, cw, lastW int) {
	cw, lastW = 8, 11
	return max(iw-1-11-3-cw*len(tickets)-lastW-1, 4), cw, lastW
}

func (m model) assignedTo(to int) string {
	if to == none {
		return fg(p.amber, "unassigned")
	}
	return fg(p.muted, "assigned to ") + bold(p.hue[to], tickets[to].key)
}

func (m model) eventsIn(s segment) (evs []event) {
	for _, e := range m.d.events {
		if e.min >= s.from && e.min < s.to {
			evs = append(evs, e)
		}
	}
	return evs
}

func (m model) stretchBox(w, h int) []string {
	// One row per stretch, one column per ticket, so a stretch shared by
	// several tickets needs no label that can be cut, and each column adds
	// up to its ticket's total. The selected row is spelled out at the foot.
	iw := w - 2
	whatW, cw, lastW := stretchCols(iw)
	cell := func(n, width int, c rgb) string {
		if n == 0 {
			return fg(p.faint, fmt.Sprintf("%*s", width, "·"))
		}
		return fg(c, fmt.Sprintf("%*s", width, dur(n)))
	}
	head := " " + fg(p.dim, fmt.Sprintf("%-11s", "time")) + "   " + pad("", whatW)
	for t, tk := range tickets {
		head += bold(p.hue[t], fmt.Sprintf("%*s", cw, tk.key))
	}
	head += fg(p.dim, fmt.Sprintf("%*s", lastW, "unassigned"))
	body := []string{head}
	var rows []string
	offSum := 0
	var title, detail string
	for i, s := range m.segments {
		per := make([]int, len(tickets))
		off, mark, name := 0, " ", ""
		evs := m.eventsIn(s)
		switch o := s.owners[0]; {
		case o >= meet:
			mt, to := meetings[o-meet], m.meetTo[o-meet]
			mark, name = fg(tone(m.eff(s.from)), "▰"), fg(p.text, mt.title)
			if to == none {
				mark = fg(p.amber, "▰")
				off = s.to - s.from
			} else {
				per[to] = s.to - s.from
			}
			title = fg(p.amber, mt.title) + fg(p.text, " · "+dur(s.to-s.from))
			more := fg(p.muted, "meeting · ") + m.assignedTo(to)
			if to == none && mt.suggest != none {
				more += fg(p.dim, " · its title names ") + fg(p.hue[mt.suggest], tickets[mt.suggest].key)
			}
			if mt.during > 0 {
				more += fg(p.amber, " · "+plural(mt.during, "event")+" fired during it")
			}
			if i == m.cur {
				detail = more
			}
		case o == gap:
			to := m.gapTo[s.gapIdx]
			mark, name = fg(tone(m.eff(s.from)), "░"), fg(p.muted, "gap")
			if to == none {
				off = s.to - s.from
			} else {
				per[to] = s.to - s.from
			}
			title = fg(p.text, "Gap · "+dur(s.to-s.from))
			if i == m.cur {
				detail = fg(p.muted, "no evidence · ") + m.assignedTo(to)
			}
		default:
			var parts []string
			for _, t := range s.owners {
				per[t] = m.ownedIn(t, s.from, s.to)
				parts = append(parts, fg(p.hue[t], tickets[t].key)+" "+fg(p.text, dur(per[t])))
			}
			name = fg(p.dim, plural(len(evs), "event"))
			title = fg(p.text, clock(s.from)+"–"+clock(s.to)+" · "+dur(s.to-s.from))
			if i == m.cur {
				detail = strings.Join(parts, fg(p.dim, " · ")) + fg(p.dim, " · from "+plural(len(evs), "event"))
			}
		}
		offSum += off
		row := clock(s.from) + "–" + clock(s.to) + " " + mark + " " + pad(name, whatW)
		for t := range tickets {
			row += cell(per[t], cw, p.text)
		}
		row += cell(off, lastW, p.amber)
		if i == m.cur {
			row = on(p.text, &p.sel, pad(bold(p.text, "›")+fg(p.text, row), iw))
		} else {
			row = " " + fg(p.text, row)
		}
		if i == m.cur {
			detail = title + "\n" + detail
		}
		rows = append(rows, row)
	}
	tot := m.totals()
	handRow := m.handRow(m.byHand(), cw, whatW, lastW)
	room := h - 2 - 6 // header, rule, total, blank, two detail lines
	if handRow != "" {
		room--
	}
	first := max(m.cur-room+1, 0)
	rows = rows[first:]
	more := 0
	if len(rows) > room {
		more = len(rows) - room
		rows = rows[:room]
	}
	body = append(body, rows...)
	if handRow != "" {
		body = append(body, handRow)
	}
	foot := " " + fg(p.dim, fmt.Sprintf("%-11s", "total")) + "   " + pad("", whatW)
	for t := range tickets {
		foot += bold(p.hue[t], fmt.Sprintf("%*s", cw, dur(tot[t])))
	}
	foot += bold(p.amber, fmt.Sprintf("%*s", lastW, dur(offSum)))
	rule := " " + fg(p.faint, strings.Repeat("─", iw-2))
	if first+more > 0 {
		hidden := fmt.Sprintf("%d above · %d below ", first, more)
		rule = " " + fg(p.dim, hidden) + fg(p.faint, strings.Repeat("─", max(iw-2-lipgloss.Width(hidden), 0)))
	}
	body = append(body, rule, foot)
	for len(body) < h-4 {
		body = append(body, "")
	}
	t, dt, _ := strings.Cut(detail, "\n")
	body = append(body, " "+t, " "+dt)
	right := fg(p.dim, fmt.Sprintf("%d of %d", m.cur+1, len(m.segments)))
	return box(2, "stretches", right, m.events == 0, w, h, body)
}

// handRow is the stretch table's row for hand adjustments, so each column
// still adds up to its total. Empty when nothing was adjusted.
func (m model) handRow(hand []int, cw, whatW, lastW int) string {
	row, some := " "+fg(p.amber, fmt.Sprintf("%-11s", "by hand"))+"   "+pad("", whatW), false
	for t := range tickets {
		n := hand[t]
		cell := fg(p.faint, fmt.Sprintf("%*s", cw, "·"))
		if n != 0 {
			some = true
			cell = fg(p.amber, fmt.Sprintf("%*s", cw, signed(n)))
		}
		row += cell
	}
	if !some {
		return ""
	}
	return row + fg(p.faint, fmt.Sprintf("%*s", lastW, "·"))
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

// eventBox audits the selected stretch. It leads with what the stretch is,
// who it was credited to and from which sessions, and where the idle-gap
// rule came close to splitting it; the raw events sit one level deeper.
func (m model) eventBox(w, h int) []string {
	s := m.segments[m.cur]
	iw := w - 2
	evs := m.eventsIn(s)
	var body []string

	head := " " + bold(p.text, clock(s.from)+"–"+clock(s.to)) + fg(p.muted, " · "+dur(s.to-s.from)+" · ")
	switch o := s.owners[0]; {
	case o >= meet:
		mt := meetings[o-meet]
		head += fg(p.amber, mt.title) + fg(p.muted, " · meeting · ") + m.assignedTo(m.meetTo[o-meet])
	case o == gap:
		head += fg(p.muted, "gap · no evidence · ") + m.assignedTo(m.gapTo[s.gapIdx])
	default:
		head += fg(p.muted, "work · "+plural(len(evs), "event"))
	}
	body = append(body, head, "")

	// who was credited, and where each ticket was found
	for t, tk := range tickets {
		counted, prompts, commits := 0, 0, 0
		for mm := s.from; mm < s.to; mm++ {
			if m.eff(mm) == t {
				counted++
			}
		}
		for _, e := range evs {
			if e.ticket == t {
				if _, k := kind(e); k == "commit" {
					commits++
				} else {
					prompts++
				}
			}
		}
		if counted == 0 && prompts+commits == 0 {
			continue
		}
		line := " " + pad(bold(p.hue[t], tk.key), 9) + fg(p.text, fmt.Sprintf("%7s", dur(counted))) + fg(p.muted, " counted")
		if prompts+commits > 0 {
			src := plural(prompts, "prompt")
			if commits > 0 {
				src += ", " + plural(commits, "commit") + " (shown, not counted)"
			}
			pre, post, _ := strings.Cut(branch[t], tk.key)
			line += fg(p.dim, " · "+src+" · branch ") + fg(p.muted, pre) + bold(p.hue[t], tk.key) + fg(p.muted, post)
		} else {
			line += fg(p.dim, " · assigned by hand")
		}
		body = append(body, line)
	}
	if o := s.owners[0]; o >= meet && meetings[o-meet].during > 0 {
		var who []string
		for _, e := range evs {
			who = append(who, tickets[e.ticket].key+" at "+clock(e.min))
		}
		body = append(body, " "+fg(p.amber, "! "+plural(len(evs), "event")+" fired during it ("+strings.Join(who, ", ")+")")+fg(p.muted, "; the meeting wins until you assign it"))
	}

	// boundary sensitivity: the longest silence that still joined
	worst, at := 0, 0
	for i := 1; i < len(evs); i++ {
		if d := evs[i].min - evs[i-1].min; d > worst {
			worst, at = d, evs[i].min
		}
	}
	switch {
	case len(evs) < 2:
	case worst >= idleGap-2:
		body = append(body, " "+fg(p.amber, fmt.Sprintf("! longest silence +%dm before %s", worst, clock(at)))+fg(p.muted, fmt.Sprintf(", %s inside the %dm idle gap; a slower prompt would have split this stretch", dur(idleGap-worst), idleGap)))
	default:
		body = append(body, " "+fg(p.dim, fmt.Sprintf("longest silence +%dm, well inside the %dm idle gap", worst, idleGap)))
	}

	// the stretch in the main screen's grammar: per ticket, a mark per event
	// over the minutes counted for it
	g := iw - labelW - 1
	col := func(mm int) int { return min((mm-s.from)*g/(s.to-s.from), g-1) }
	mid := func(c int) int { return s.from + (2*c+1)*(s.to-s.from)/(2*g) }
	axis := []rune(strings.Repeat(" ", g))
	for mm := (s.from + 9) / 10 * 10; mm < s.to; mm += 10 {
		if c := col(mm); c+5 <= g && (c == 0 || axis[c-1] == ' ') {
			copy(axis[c:], []rune(clock(mm)))
		}
	}
	var lanes []string
	for t, tk := range tickets {
		marks := make([]string, g)
		seen, counted := false, false
		for _, e := range evs {
			if e.ticket == t {
				gl, _ := kind(e)
				marks[col(e.min)] = gl
				seen = true
			}
		}
		var hb, cb strings.Builder
		for c := range marks {
			if marks[c] == "" {
				hb.WriteString(fg(p.faint, "·"))
			} else {
				hb.WriteString(bold(p.hue[t], marks[c]))
			}
			if m.eff(mid(c)) == t {
				cb.WriteString(fg(p.hue[t], "━"))
				counted = true
			} else {
				cb.WriteString(fg(p.faint, "─"))
			}
		}
		if seen || counted {
			lanes = append(lanes, pad(" "+bold(p.hue[t], tk.key), labelW)+hb.String(), strings.Repeat(" ", labelW)+cb.String())
		}
	}
	if len(lanes) > 0 {
		body = append(body, "", strings.Repeat(" ", labelW)+fg(p.dim, string(axis)))
		body = append(body, lanes...)
	}

	if m.events < 2 {
		body = append(body, "", " "+fg(p.dim, "e again lists every event with the silence before it · esc closes"))
		return box(2, "inspect", fg(p.dim, plural(len(evs), "event")), true, w, h, body)
	}

	// every event, reading across: time, kind, and the silence before it
	body = append(body, "")
	const cellW = 15
	per := max((iw-2)/cellW, 1)
	room := (h - 2 - len(body)) * per
	var line strings.Builder
	for i, e := range evs {
		if i == room-1 && len(evs) > room {
			line.WriteString(fg(p.dim, fmt.Sprintf("+ %d more", len(evs)-i)))
			break
		}
		gl, _ := kind(e)
		since := ""
		if i > 0 {
			d := e.min - evs[i-1].min
			c := p.dim
			if d >= idleGap-2 {
				c = p.amber
			}
			since = fg(c, fmt.Sprintf("+%dm", d))
		}
		line.WriteString(pad(fg(p.text, clock(e.min))+" "+bold(p.hue[e.ticket], gl)+" "+since, cellW))
		if (i+1)%per == 0 {
			body = append(body, " "+line.String())
			line.Reset()
		}
	}
	if line.Len() > 0 {
		body = append(body, " "+line.String())
	}
	if len(evs) == 0 {
		body = append(body, " "+fg(p.dim, "nothing fired in this stretch"))
	}
	legend := fg(p.muted, "│ prompt  ◆ commit  ") + fg(p.amber, "+9m") + fg(p.muted, " near the idle gap")
	return box(2, "inspect · every event", legend, true, w, h, body)
}

// --- 3 draft: the worklogs that would be sent ---

func (m model) draftBox(w, h int) []string {
	iw := w - 2
	line := func(left, right string) string { return " " + spread(left, right, iw-2) }
	meetOff, gapOff := m.unassigned()
	ruled := h-2 >= len(tickets)*3+3
	hand := m.byHand()
	var body []string
	for t, tk := range tickets {
		key := bold(p.hue[t], tk.key)
		if n := hand[t]; n != 0 {
			key += fg(p.amber, "  "+signed(n)+" by hand")
		}
		body = append(body, line(key, bold(p.text, dur(m.total(t)))), " "+fg(p.muted, tk.summary))
		if ruled {
			body = append(body, " "+fg(p.faint, strings.Repeat("─", iw-2)))
		}
	}
	if !ruled {
		body = append(body, " "+fg(p.faint, strings.Repeat("─", iw-2)))
	}
	body = append(body,
		line(bold(p.text, "ready to send"), bold(p.text, dur(m.toSend()))),
		line(fg(p.muted, "unassigned"), fg(p.amber, dur(meetOff+gapOff))))
	return box(3, "draft", fg(p.dim, plural(len(tickets), "worklog")), false, w, h, body)
}

func (m model) View() tea.View {
	p = &themes[m.theme]
	var out []string
	if m.w < 96 || m.h < 30 {
		out = []string{fmt.Sprintf("PROTOTYPE: needs 96×30 or larger (now %d×%d)", m.w, m.h)}
	} else {
		evRows := 2
		if m.h >= 44 {
			evRows = 3
		}
		sep := m.h >= 34
		topH := dayHeight(evRows, sep)
		brand := " " + bold(p.text, "sift") + " " + fg(p.focus, "▰") + "   " + fg(p.text, "Tue 10 Nov") + fg(p.dim, " · draft")
		out = append(out, spread(brand, fg(p.muted, fmt.Sprintf("%d tickets · %s to send ", len(tickets), dur(m.toSend()))), m.w))
		out = append(out, box(1, "day", fg(p.dim, "evidence and counted time"), false, m.w, topH, m.dayBox(m.w, evRows, sep))...)
		botH := m.h - topH - 2
		rw := max(34, min(46, m.w*36/100))
		if m.events > 0 {
			out = append(out, m.eventBox(m.w, botH)...)
		} else {
			l := m.stretchBox(m.w-rw, botH)
			r := m.draftBox(rw, botH)
			for i := range l {
				out = append(out, l[i]+r[i])
			}
		}
		key := func(k, what string) string { return fg(p.text, k) + fg(p.dim, " "+what+"   ") }
		keys := " " + key("↑↓", "select") + key("e", "inspect") + key("t", "assign") + key("+ −", "adjust 5m") + key("T", p.name) + key("q", "quit")
		out = append(out, spread(keys, fg(p.faint, "prototype · fake data "), m.w))
	}
	v := tea.NewView(strings.Join(out, "\n"))
	v.AltScreen = true
	v.BackgroundColor = lipgloss.Color(p.bg.String())
	v.ForegroundColor = lipgloss.Color(p.text.String())
	return v
}

func main() {
	if _, err := tea.NewProgram(newModel()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
