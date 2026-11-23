package tui

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rockylabsdev/sift/internal/derive"
	"github.com/rockylabsdev/sift/internal/draft"
)

const labelW = 10

func (m Model) hue(t string) rgb {
	th := m.theme_()
	for i, x := range m.tickets {
		if x == t {
			return th.hue[i%len(th.hue)]
		}
	}
	return th.muted
}

// span is the hours the day box draws: from the hour of the first stretch
// to the hour after the last, and on a live day through the current hour,
// at least eight hours.
func (m Model) span() (from, to int) {
	from, to = 8*60, 18*60
	if s := m.stretches(); len(s) > 0 {
		from = m.minute(s[0].From) / 60 * 60
		to = (m.minute(s[len(s)-1].To) + 59) / 60 * 60
	}
	if m.live() {
		h := m.minute(m.in.Now)
		from, to = min(from, h/60*60), min(max(to, (h+30+59)/60*60), m.dayLen())
	}
	for to-from < 8*60 {
		if to < m.dayLen() {
			to += 60
		}
		if to-from < 8*60 && from > 0 {
			from -= 60
		}
		if from == 0 && to >= m.dayLen() {
			break
		}
	}
	return from, to
}

func (m Model) clock(min int) string {
	return m.in.Day.Start.Add(time.Duration(min) * time.Minute).Format("15:04")
}

func (m Model) clockT(t time.Time) string { return t.In(m.in.Start.Location()).Format("15:04") }

// --- 1 day ---

// laneLayout picks density rows per lane and separators to fit h rows.
func (m Model) laneLayout() (evRows int, sep bool, shown int) {
	n := len(m.tickets)
	room := m.h - 2 - 13 // header, footer, and a bottom panel of 13
	for _, c := range []struct {
		ev  int
		sep bool
	}{{3, true}, {2, true}, {2, false}, {1, false}} {
		if c.ev == 3 && m.h < 44 {
			continue
		}
		if dayHeight(n, c.ev, c.sep, m.labelled()) <= room {
			return c.ev, c.sep, n
		}
	}
	room -= dayHeight(0, 1, false, m.labelled()) - dayHeight(0, 1, false, false)
	shown = max((room-6)/2, 1) // one density row and a baseline each, then "+N more"
	return 1, false, min(shown, n)
}

// labelled is whether the day box gives meeting names a row of their own.
func (m Model) labelled() bool {
	for _, s := range m.list {
		if s.Kind == derive.InMeet {
			return true
		}
	}
	return false
}

func dayHeight(lanes, evRows int, sep, labels bool) int {
	h := 2 + 1 + lanes*(evRows+1) + 3
	if labels {
		h++
	}
	if sep && lanes > 1 {
		h += lanes - 1
	}
	return max(h, 9)
}

func (m Model) dayBox(w int) []string {
	th := m.theme_()
	evRows, sep, shown := m.laneLayout()
	from, to := m.span()
	g := w - 2 - labelW - 1
	col := func(c int) (int, int) { return from + c*(to-from)/g, from + (c+1)*(to-from)/g }
	selRow, hasSel := m.selectedRow()
	sel := selRow.Stretch
	// on a live day the strip ends at now, marked by a tinted column
	nowCol, nowMin := g, m.dayLen()
	if m.live() {
		nowMin = m.minute(m.in.Now)
		nowCol = (nowMin - from) * g / (to - from)
	}
	bgAt := func(c int) *rgb {
		a, b := col(c)
		if c == nowCol {
			return &th.border
		}
		if mid := (a + b) / 2; hasSel && mid >= m.minute(sel.From) && mid < m.minute(sel.To) {
			return &th.sel
		}
		return nil
	}

	var out []string
	axis := []rune(strings.Repeat(" ", g))
	later := g // where the hour labels at or after now begin
	step := 60
	if (to-from)/60 > 10 {
		step = 120
	}
	for h := from; h < to; h += step {
		if c := (h - from) * g / (to - from); c+2 <= g {
			copy(axis[c:], []rune(m.in.Day.Start.Add(time.Duration(h)*time.Minute).Format("15")))
			if h >= nowMin && later == g {
				later = c
			}
		}
	}
	axisRow := fg(th.dim, string(axis))
	if nowCol < g {
		var b strings.Builder
		for c, r := range axis {
			tone := th.dim
			if c >= later {
				tone = th.faint
			}
			b.WriteString(on(tone, bgAt(c), string(r)))
		}
		axisRow = b.String()
	}
	out = append(out, strings.Repeat(" ", labelW)+axisRow)

	if len(m.stretches()) == 0 {
		return append(out, "", " "+fg(th.muted, "No evidence on "+m.in.Start.Format("Mon 2 Jan")+"."),
			" "+fg(th.dim, "Prompts you type in Claude Code are recorded here as you work."))
	}

	// event density on one scale shared by every lane, two samples a column
	n := 2 * g
	val := make([][]float64, len(m.tickets))
	peak := 1.0
	idx := map[string]int{}
	for i, t := range m.tickets {
		idx[t] = i
		val[i] = make([]float64, n)
	}
	for _, e := range m.in.Events {
		if e.Ticket == nil || !e.Origin.IsEvidence() {
			continue
		}
		i, ok := idx[*e.Ticket]
		if !ok {
			continue
		}
		at := float64(m.minute(e.At) - from)
		for s := max(int((at-6)*float64(n)/float64(to-from)), 0); s < n; s++ {
			x := float64(s) * float64(to-from) / float64(n)
			if x > at+6 {
				break
			}
			if d := abs(x - at); d < 6 {
				val[i][s] += 1 - d/6
			}
		}
	}
	for i := range val {
		for _, v := range val[i] {
			peak = max(peak, v)
		}
	}

	nowLine := ""
	if nowCol < g {
		nowLine = strings.Repeat(" ", labelW+nowCol) + on(th.faint, &th.border, " ")
	}
	left := []rune{0x40, 0x04, 0x02, 0x01}
	right := []rune{0x80, 0x20, 0x10, 0x08}
	for i, t := range m.tickets[:shown] {
		if i > 0 && sep {
			out = append(out, nowLine)
		}
		h := m.hue(t)
		for r := 0; r < evRows; r++ {
			label := ""
			switch {
			case r == 0:
				label = " " + bold(h, t)
			case r == 1:
				label = " " + fg(th.text, dur(m.worklog(t).Minutes))
			}
			var b strings.Builder
			for c := 0; c < g; c++ {
				ch := rune(0x2800)
				for side, dots := range [][]rune{left, right} {
					lvl := int(val[i][2*c+side]/peak*float64(4*evRows) + 0.99)
					for k := 0; k < min(max(lvl-(evRows-1-r)*4, 0), 4); k++ {
						ch |= dots[k]
					}
				}
				if ch == 0x2800 {
					ch = ' '
				}
				b.WriteString(on(h, bgAt(c), string(ch)))
			}
			out = append(out, pad(label, labelW)+b.String())
		}
		label := ""
		if evRows == 1 {
			label = " " + fg(th.text, dur(m.worklog(t).Minutes))
		}
		var b strings.Builder
		for c := 0; c < g; c++ {
			if c > nowCol {
				b.WriteString(on(th.faint, bgAt(c), " "))
				continue
			}
			a, z := col(c)
			own, agent := 0, 0
			for mm := a; mm < z && mm < len(m.effT); mm++ {
				if m.effT[mm] == t {
					own++
					if m.effA[mm] {
						agent++
					}
				}
			}
			switch {
			case own*2 >= max(z-a, 1) && agent*2 > own:
				b.WriteString(on(h, bgAt(c), "┅"))
			case own*2 >= max(z-a, 1):
				b.WriteString(on(h, bgAt(c), "━"))
			default:
				b.WriteString(on(th.faint, bgAt(c), "─"))
			}
		}
		out = append(out, pad(label, labelW)+b.String())
	}
	if shown < len(m.tickets) {
		out = append(out, " "+fg(th.dim, fmt.Sprintf("+ %d more tickets, in the draft", len(m.tickets)-shown)))
	}

	// meetings, gaps and ticketless time: amber or dim until assigned
	other := make([]string, g)
	for c := range other {
		a, z := col(c)
		mid := min((a+z)/2, len(m.effK)-1)
		k, t := m.effK[mid], m.effT[mid]
		tone := th.amber
		if t != "" {
			tone = m.hue(t)
		}
		switch {
		case k == derive.InMeet:
			other[c] = on(tone, bgAt(c), "▰")
		case k == derive.Silence:
			if t == "" {
				tone = th.dim
			}
			other[c] = on(tone, bgAt(c), "░")
		case k == derive.Work && t == "":
			other[c] = on(th.amber, bgAt(c), "?")
		default:
			other[c] = on(th.faint, bgAt(c), " ")
		}
	}
	// Each column shows the meeting at its middle minute, as the strip
	// samples it. A meeting that runs into the next ends a column early, so
	// back-to-back meetings read as separate bars.
	var meets []row
	for _, r := range m.list {
		if r.Kind == derive.InMeet {
			meets = append(meets, r)
		}
	}
	meetAt := make([]int, g)
	for c := range meetAt {
		a, z := col(c)
		meetAt[c] = -1
		for i, s := range meets {
			if mid := (a + z) / 2; mid >= m.minute(s.From) && mid < m.minute(s.To) {
				meetAt[c] = i
			}
		}
	}
	for c := range meetAt {
		// a skipped meeting is an outline wherever the evidence left the column empty
		a, z := col(c)
		if i := meetAt[c]; i >= 0 && meets[i].skipped && m.effK[min((a+z)/2, len(m.effK)-1)] == "" {
			other[c] = on(th.dim, bgAt(c), "▱")
		}
	}
	for c := 1; c+1 < g; c++ {
		if i := meetAt[c]; i >= 0 && meetAt[c-1] == i && meetAt[c+1] >= 0 && meetAt[c+1] != i {
			other[c] = on(th.faint, bgAt(c), " ")
		}
	}
	// Names go on their own row, from where each meeting starts, whole or
	// not at all, two columns clear of the name before. A name may run past
	// its bar only over columns no other meeting holds, so it never sits
	// above a meeting it does not name.
	labels := make([]string, g)
	for c := range labels {
		labels[c] = on(th.faint, bgAt(c), " ")
	}
	free := 0
	for i, s := range meets {
		c0 := slices.Index(meetAt, i)
		name := []rune(s.Meeting)
		if c0 < free || c0+len(name) > min(g, nowCol+1) || slices.ContainsFunc(meetAt[c0:c0+len(name)], func(j int) bool { return j >= 0 && j != i }) {
			continue
		}
		tone := th.amber
		if t := m.in.Decisions.Assign[draft.ID(s.Stretch)]; t != "" {
			tone = m.hue(t)
		}
		if s.skipped {
			tone = th.dim
		}
		for j, r := range name {
			labels[c0+j] = on(tone, bgAt(c0+j), string(r))
		}
		free = c0 + len(name) + 2
	}
	if len(meets) > 0 {
		out = append(out, strings.Repeat(" ", labelW)+strings.Join(labels, ""))
	}
	for c := nowCol + 1; c < g; c++ {
		other[c] = on(th.faint, bgAt(c), " ")
	}
	out = append(out, pad(" "+fg(th.dim, "other"), labelW)+strings.Join(other, ""))

	if hasSel {
		a := max((m.minute(sel.From)-from)*g/(to-from), 0)
		z := max(((m.minute(sel.To)-from)*g+(to-from)-1)/(to-from)-1, a)
		bracket := "↑"
		if z > a {
			bracket = "└" + strings.Repeat("─", z-a-1) + "┘"
		}
		when := m.clockT(sel.From) + "–" + m.clockT(sel.To)
		if sel.Kind == derive.InMeet {
			when += " " + sel.Meeting
		}
		mark := strings.Repeat(" ", a) + bracket + " " + when
		if z+2+lipgloss.Width(when) > g {
			mark = strings.Repeat(" ", max(a-lipgloss.Width(when)-1, 0)) + when + " " + bracket
		}
		out = append(out, strings.Repeat(" ", labelW)+fg(th.focus, mark))
	}
	u := m.dr.Unassigned
	sum := " " + fg(th.muted, "unassigned") + "  " + fg(th.amber, "▰ meetings "+dur(u.Meetings)) + fg(th.dim, "  ·  ") + fg(th.muted, "░ gaps "+dur(u.Gaps))
	if u.Ticketless > 0 {
		sum += fg(th.dim, "  ·  ") + fg(th.amber, "? no ticket "+dur(u.Ticketless))
	}
	note := fg(th.dim, "⣿ your prompts  ") + fg(th.muted, "━ you  ┅ agents ")
	if lipgloss.Width(sum)+lipgloss.Width(note)+1 > w-2 {
		note = fg(th.muted, "━ you  ┅ agents ")
	}
	return append(out, spread(sum, note, w-2))
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// --- 2 stretches ---

func stretchCols(iw, tickets int) (whatW, cw, lastW int) {
	cw, lastW = 8, 11
	return max(iw-1-11-3-cw*tickets-lastW-1, 4), cw, lastW
}

func (m Model) assignedTo(t string) string {
	th := m.theme_()
	if t == "" {
		return fg(th.amber, "unassigned")
	}
	return fg(th.muted, "assigned to ") + bold(m.hue(t), t)
}

// eventsIn counts the user's own events in s; agent activity is reported
// as minutes, not events.
func (m Model) eventsIn(s derive.Stretch) (n int) {
	for _, e := range m.in.Events {
		if e.Origin.IsEvidence() && !e.At.Before(s.From) && e.At.Before(s.To) {
			n++
		}
	}
	return n
}

// agentIn is the agent minutes counted in s and how many subagents ran.
func (m Model) agentIn(s derive.Stretch) (minutes, subagents int) {
	for _, x := range s.Agent {
		minutes += x
	}
	seen := map[string]bool{}
	for _, e := range m.in.Events {
		if !e.Origin.IsEvidence() && e.AgentID != "" && !e.At.Before(s.From) && e.At.Before(s.To) {
			seen[e.AgentID] = true
		}
	}
	return minutes, len(seen)
}

// tableTickets is the tickets with a column: as many as fit.
func (m Model) tableTickets(iw int) []string {
	n := len(m.tickets)
	for n > 0 {
		if w, _, _ := stretchCols(iw, n); w >= 8 {
			break
		}
		n--
	}
	return m.tickets[:n]
}

func (m Model) stretchBox(w, h int) []string {
	th := m.theme_()
	iw := w - 2
	cols := m.tableTickets(iw)
	whatW, cw, lastW := stretchCols(iw, len(cols))
	cell := func(n, width int, c rgb) string {
		if n == 0 {
			return fg(th.faint, fmt.Sprintf("%*s", width, "·"))
		}
		return fg(c, fmt.Sprintf("%*s", width, dur(n)))
	}
	head := " " + fg(th.dim, fmt.Sprintf("%-11s", "time")) + "   " + pad("", whatW)
	for _, t := range cols {
		head += bold(m.hue(t), fmt.Sprintf("%*s", cw, ansiCut(t, cw-1)))
	}
	head += fg(th.dim, fmt.Sprintf("%*s", lastW, "unassigned"))
	body := []string{head}
	var rows []string
	var title, detail string
	offSum := 0
	for i, r := range m.list {
		s := r.Stretch
		if r.skipped {
			line := m.clockT(s.From) + "–" + m.clockT(s.To) + " " + fg(th.dim, "▱") + " " + pad(strike(th.dim, s.Meeting), whatW)
			for range cols {
				line += fg(th.faint, fmt.Sprintf("%*s", cw, "·"))
			}
			line += fg(th.dim, fmt.Sprintf("%*s", lastW, "skipped"))
			if i == m.cur {
				line = on(th.text, &th.sel, pad(bold(th.text, "›")+fg(th.text, line), iw))
				detail = fg(th.muted, s.Meeting+" · "+dur(s.Minutes())) + "\n" + fg(th.muted, "meeting · skipped, so its time goes to the evidence · ") + fg(th.text, "x") + fg(th.muted, " counts it again")
			} else {
				line = " " + fg(th.text, line)
			}
			rows = append(rows, line)
			continue
		}
		per := map[string]int{}
		off, mark, name := 0, " ", ""
		assigned := m.in.Decisions.Assign[draft.ID(s)]
		n, _ := draft.Assignable(s)
		if assigned != "" {
			per[assigned] += n
		} else {
			off = n
		}
		tone := th.amber
		if assigned != "" {
			tone = m.hue(assigned)
		}
		var more string
		switch s.Kind {
		case derive.InMeet:
			mark, name = fg(tone, "▰"), fg(th.text, s.Meeting)
			title = fg(th.amber, s.Meeting) + fg(th.text, " · "+dur(s.Minutes()))
			more = fg(th.muted, "meeting · ") + m.assignedTo(assigned)
			if sg := suggestion(s); assigned == "" && sg != "" {
				more += fg(th.dim, " · its title names ") + bold(m.hue(sg), sg)
			}
			if e := m.eventsIn(s); e > 0 {
				more += fg(th.amber, " · "+plural(e, "event")+" fired during it") + fg(th.muted, "; x if you were not in it")
			}
		case derive.Silence:
			if assigned == "" {
				tone = th.dim
			}
			mark, name = fg(tone, "░"), fg(th.muted, "gap")
			title = fg(th.text, "Gap · "+dur(s.Minutes()))
			more = fg(th.muted, "no evidence · ") + m.assignedTo(assigned)
		default:
			for t, x := range s.Tickets {
				per[t] += x
			}
			ev := m.eventsIn(s)
			name = fg(th.dim, plural(ev, "event"))
			if am, _ := m.agentIn(s); am > 0 {
				name += fg(th.dim, " · agents "+dur(am))
			}
			if from := m.movedFrom(s); from != "" {
				for _, hint := range []string{" ← " + from, " · moved"} {
					if lipgloss.Width(name+hint) <= whatW {
						name += fg(th.muted, hint)
						break
					}
				}
			}
			if s.Unassigned > 0 {
				mark = fg(tone, "?")
			}
			title = fg(th.text, m.clockT(s.From)+"–"+m.clockT(s.To)+" · "+dur(s.Minutes()))
			var parts []string
			for _, t := range m.tickets {
				if per[t] > 0 && s.Tickets[t] > 0 {
					parts = append(parts, fg(m.hue(t), t)+" "+fg(th.text, dur(s.Tickets[t])))
				}
			}
			if s.Unassigned > 0 {
				parts = append(parts, fg(th.amber, "no ticket "+dur(s.Unassigned))+fg(th.muted, ", ")+m.assignedTo(assigned))
			}
			more = strings.Join(parts, fg(th.dim, " · ")) + fg(th.dim, " · from "+plural(ev, "event"))
			if am, subs := m.agentIn(s); am > 0 {
				more += fg(th.dim, " · agents "+dur(am))
				if subs > 0 {
					more += fg(th.dim, ", "+plural(subs, "subagent"))
				}
			}
		}
		offSum += off
		row := m.clockT(s.From) + "–" + m.clockT(s.To) + " " + mark + " " + pad(name, whatW)
		for _, t := range cols {
			row += cell(per[t], cw, th.text)
		}
		row += cell(off, lastW, th.amber)
		if i == m.cur {
			row = on(th.text, &th.sel, pad(bold(th.text, "›")+fg(th.text, row), iw))
			detail = title + "\n" + more
		} else {
			row = " " + fg(th.text, row)
		}
		rows = append(rows, row)
	}
	hand := ""
	for _, t := range cols {
		if m.worklog(t).ByHand != 0 {
			hand = " " + fg(th.amber, fmt.Sprintf("%-11s", "by hand")) + "   " + pad("", whatW)
			for _, t := range cols {
				if b := m.worklog(t).ByHand; b != 0 {
					hand += fg(th.amber, fmt.Sprintf("%*s", cw, signed(b)))
				} else {
					hand += fg(th.faint, fmt.Sprintf("%*s", cw, "·"))
				}
			}
			hand += fg(th.faint, fmt.Sprintf("%*s", lastW, "·"))
			break
		}
	}
	room := h - 2 - 6
	if hand != "" {
		room--
	}
	first := max(m.cur-room+1, 0)
	rows = rows[min(first, len(rows)):]
	more := 0
	if len(rows) > room {
		more, rows = len(rows)-room, rows[:max(room, 0)]
	}
	body = append(body, rows...)
	if len(m.list) == 0 {
		body = append(body, " "+fg(th.dim, "Nothing to review yet."))
	}
	if hand != "" {
		body = append(body, hand)
	}
	rule := " " + fg(th.faint, strings.Repeat("─", iw-2))
	if first+more > 0 {
		hidden := fmt.Sprintf("%d above · %d below ", first, more)
		rule = " " + fg(th.dim, hidden) + fg(th.faint, strings.Repeat("─", max(iw-2-lipgloss.Width(hidden), 0)))
	}
	foot := " " + fg(th.dim, fmt.Sprintf("%-11s", "total")) + "   " + pad("", whatW)
	for _, t := range cols {
		foot += bold(m.hue(t), fmt.Sprintf("%*s", cw, dur(m.worklog(t).Minutes)))
	}
	foot += bold(th.amber, fmt.Sprintf("%*s", lastW, dur(offSum)))
	body = append(body, rule, foot)
	for len(body) < h-4 {
		body = append(body, "")
	}
	t, dt, _ := strings.Cut(detail, "\n")
	body = append(body, " "+t, " "+dt)
	right := fg(th.dim, fmt.Sprintf("%d of %d", min(m.cur+1, len(m.list)), len(m.list)))
	if extra := len(m.tickets) - len(cols); extra > 0 {
		right = fg(th.dim, fmt.Sprintf("%d tickets in the draft only · ", extra)) + right
	}
	return th.box(2, "stretches", right, m.inspect == 0, w, h, body)
}

func ansiCut(s string, w int) string {
	if len(s) <= w {
		return s
	}
	return s[:w-1] + "…"
}

// --- inspect ---

func (m Model) inspectBox(w, h int) []string {
	th := m.theme_()
	r, ok := m.selectedRow()
	s := r.Stretch
	if !ok {
		return th.box(2, "inspect", "", true, w, h, []string{" " + fg(th.dim, "Nothing to inspect.")})
	}
	iw := w - 2
	var evs []int // the user's own events; agent activity is summed below
	agentEvents, subagents := 0, map[string]bool{}
	for i, e := range m.in.Events {
		if e.At.Before(s.From) || !e.At.Before(s.To) {
			continue
		}
		if e.Origin.IsEvidence() {
			evs = append(evs, i)
		} else {
			agentEvents++
			if e.AgentID != "" {
				subagents[e.AgentID] = true
			}
		}
	}
	assigned := m.in.Decisions.Assign[draft.ID(s)]
	head := " " + bold(th.text, m.clockT(s.From)+"–"+m.clockT(s.To)) + fg(th.muted, " · "+dur(s.Minutes())+" · ")
	switch s.Kind {
	case derive.InMeet:
		head += fg(th.amber, s.Meeting) + fg(th.muted, " · meeting · ")
		if r.skipped {
			head += fg(th.muted, "skipped")
		} else {
			head += m.assignedTo(assigned)
		}
	case derive.Silence:
		head += fg(th.muted, "gap · no evidence · ") + m.assignedTo(assigned)
	default:
		head += fg(th.muted, "work · "+plural(len(evs), "event"))
	}
	body := []string{head, ""}

	// who was credited, and where each ticket was found
	type src struct {
		n       int
		via     map[string]bool
		counted int
		agent   int
	}
	per := map[string]*src{}
	get := func(t string) *src {
		if per[t] == nil {
			per[t] = &src{via: map[string]bool{}}
		}
		return per[t]
	}
	for mm := m.minute(s.From); mm < m.minute(s.To); mm++ {
		if t := m.effT[mm]; t != "" {
			get(t).counted++
			if m.effA[mm] {
				get(t).agent++
			}
		}
	}
	ticketless := 0
	for _, i := range evs {
		e := m.in.Events[i]
		if e.Ticket == nil {
			ticketless++
			continue
		}
		x := get(*e.Ticket)
		x.n++
		x.via[e.Found.Via+" "+e.Found.Value] = true
	}
	for _, t := range append(append([]string{}, m.tickets...), keysOf(per)...) {
		x := per[t]
		if x == nil {
			continue
		}
		delete(per, t)
		line := " " + pad(bold(m.hue(t), t), 10) + fg(th.text, fmt.Sprintf("%7s", dur(x.counted))) + fg(th.muted, " counted")
		if x.agent > 0 {
			line += fg(th.muted, fmt.Sprintf(" (agents %s)", dur(x.agent)))
		}
		if x.n > 0 {
			var via []string
			for v := range x.via {
				via = append(via, v)
			}
			line += fg(th.dim, " · "+plural(x.n, "prompt")+" · ") + fg(th.muted, strings.Join(via, ", "))
		} else if x.agent == 0 {
			line += fg(th.dim, " · assigned by you")
		}
		body = append(body, line)
	}
	if agentEvents > 0 {
		who := "the main agent"
		if len(subagents) > 0 {
			who = plural(len(subagents), "subagent")
		}
		body = append(body, " "+fg(th.muted, "┅ agents: ")+fg(th.dim, who+" worked while you were not prompting; this fills only time your own evidence leaves"))
	}
	if ticketless > 0 {
		body = append(body, " "+fg(th.amber, "? "+plural(ticketless, "prompt")+" named no ticket")+fg(th.muted, "; t assigns their time"))
	}
	if s.Kind == derive.InMeet && len(evs) > 0 {
		body = append(body, " "+fg(th.amber, "! "+plural(len(evs), "event")+" fired during it")+fg(th.muted, "; the meeting wins until you assign it"))
	}
	// the silence the idle-gap rule acts on is between any two pieces of
	// evidence, an agent's included: a tool run covers its whole span
	type mark struct{ from, to time.Time }
	var marks []mark
	for _, e := range m.in.Events {
		if e.At.Before(s.From) || !e.At.Before(s.To) {
			continue
		}
		marks = append(marks, mark{e.At.Add(-time.Duration(e.SpanMs) * time.Millisecond), e.At})
	}
	sort.Slice(marks, func(i, j int) bool { return marks[i].from.Before(marks[j].from) })
	worst, at, end := 0, time.Time{}, time.Time{}
	for k, x := range marks {
		if k > 0 {
			if d := int(x.from.Sub(end) / time.Minute); d > worst {
				worst, at = d, x.from
			}
		}
		if x.to.After(end) {
			end = x.to
		}
	}
	gap := m.in.IdleGap
	switch {
	case len(marks) < 2:
	case worst >= gap-2:
		body = append(body, " "+fg(th.amber, fmt.Sprintf("! longest silence +%dm before %s", worst, m.clockT(at)))+fg(th.muted, fmt.Sprintf(", %s inside the %dm idle gap; one more minute of silence would have split this stretch", dur(gap-worst), gap)))
	default:
		body = append(body, " "+fg(th.dim, fmt.Sprintf("longest silence +%dm, well inside the %dm idle gap", worst, gap)))
	}

	if m.inspect < 2 {
		body = append(body, "", " "+fg(th.dim, "e again lists every event with the silence before it · esc closes"))
		return th.box(2, "inspect", fg(th.dim, plural(len(evs), "event")), true, w, h, body)
	}
	body = append(body, "")
	const cellW = 16
	perLine := max((iw-2)/cellW, 1)
	room := (h - 2 - len(body)) * perLine
	var line strings.Builder
	for k, i := range evs {
		if k == room-1 && len(evs) > room {
			line.WriteString(fg(th.dim, fmt.Sprintf("+ %d more", len(evs)-k)))
			break
		}
		e := m.in.Events[i]
		c, mark := th.amber, "?"
		if e.Ticket != nil {
			c, mark = m.hue(*e.Ticket), "│"
		}
		since := ""
		if k > 0 {
			d := int(e.At.Sub(m.in.Events[evs[k-1]].At) / time.Minute)
			sc := th.dim
			if d >= gap-2 {
				sc = th.amber
			}
			since = fg(sc, fmt.Sprintf("+%dm", d))
		}
		line.WriteString(pad(fg(th.text, m.clockT(e.At))+" "+bold(c, mark)+" "+since, cellW))
		if (k+1)%perLine == 0 {
			body = append(body, " "+line.String())
			line.Reset()
		}
	}
	if line.Len() > 0 {
		body = append(body, " "+line.String())
	}
	if len(evs) == 0 {
		body = append(body, " "+fg(th.dim, "nothing fired in this stretch"))
	}
	legend := fg(th.muted, "│ prompt  ? no ticket  ") + fg(th.amber, fmt.Sprintf("+%dm", gap-1)) + fg(th.muted, " near the idle gap")
	return th.box(2, "inspect · every event", legend, true, w, h, body)
}

func keysOf[T any](m map[string]T) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// --- 3 draft ---

func (m Model) draftBox(w, h int) []string {
	th := m.theme_()
	iw := w - 2
	line := func(l, r string) string { return " " + spread(l, r, iw-2) }
	u := m.dr.Unassigned
	ruled := h-2 >= len(m.dr.Worklogs)*3+3
	var body []string
	for _, wl := range m.dr.Worklogs {
		key := bold(m.hue(wl.Ticket), wl.Ticket)
		if wl.ByHand != 0 {
			key += fg(th.amber, "  "+signed(wl.ByHand)+" by hand")
		}
		var incl []string
		if wl.Agent > 0 {
			incl = append(incl, dur(wl.Agent)+" agents")
		}
		if wl.Assigned > 0 {
			incl = append(incl, dur(wl.Assigned)+" assigned")
		}
		sub := ""
		if len(incl) > 0 {
			sub = "incl. " + strings.Join(incl, ", ")
		}
		second := fg(th.muted, "from "+m.clockT(wl.Started)+"  ") + fg(th.dim, sub)
		if m.dr.Confirmed {
			second = fg(th.muted, "from "+m.clockT(wl.Started)+" · ") + m.sentLine(wl.Ticket)
		}
		body = append(body, line(key, bold(th.text, dur(wl.Minutes))))
		for _, l := range strings.Split(second, "\n") {
			body = append(body, " "+l)
		}
		if ruled {
			body = append(body, " "+fg(th.faint, strings.Repeat("─", iw-2)))
		}
	}
	if len(m.dr.Worklogs) == 0 {
		body = append(body, " "+fg(th.dim, "No worklogs."))
	}
	if !ruled {
		body = append(body, " "+fg(th.faint, strings.Repeat("─", iw-2)))
	}
	label := "ready to send"
	if m.dr.Confirmed {
		label = "confirmed"
	}
	body = append(body, line(bold(th.text, label), bold(th.text, dur(m.dr.Total))),
		line(fg(th.muted, "unassigned"), fg(th.amber, dur(u.Meetings+u.Gaps+u.Ticketless))))
	if m.in.Round > 0 {
		body = append(body, " "+fg(th.dim, fmt.Sprintf("rounded to %dm per ticket", m.in.Round)))
	}
	title := "draft"
	if m.dr.Confirmed {
		title = "confirmed"
	}
	return th.box(3, title, fg(th.dim, plural(len(m.dr.Sendable()), "worklog")), false, w, h, body)
}

// ago says how far back the day is: today, yesterday, 3 days ago.
func (m Model) ago() string {
	if m.in.Today.IsZero() {
		return ""
	}
	n := int(m.in.Today.Sub(m.in.Start).Hours()/24 + 0.5)
	switch {
	case n == 0:
		return "today"
	case n == 1:
		return "yesterday"
	case n > 1:
		return fmt.Sprintf("%d days ago", n)
	}
	return "ahead"
}

// header names the day, how far back it is, and, in a coloured label with
// words, whether it is waiting for review or confirmed and read-only.
func (m Model) header() string {
	th := m.theme_()
	day := fg(th.text, m.in.Start.Format("Mon 2 Jan"))
	if a := m.ago(); a != "" {
		day += fg(th.muted, " · "+a)
	}
	if m.live() {
		day += fg(th.muted, " · ") + fg(th.focus, "live · "+m.clockT(m.in.Now))
	}
	badge := fg(th.dim, "draft")
	right := fmt.Sprintf("%s · %s to send ", plural(len(m.dr.Sendable()), "ticket"), dur(m.dr.Total))
	switch {
	case m.dr.Confirmed:
		when := ""
		if c := m.in.Decisions.Confirmed; c != nil && !c.At.IsZero() {
			when = " " + c.At.In(m.in.Start.Location()).Format("Mon 15:04")
		}
		badge = on(th.bg, &th.focus, " confirmed"+when+" · read-only ")
		right = fmt.Sprintf("%s · %s confirmed ", plural(len(m.dr.Sendable()), "worklog"), dur(m.dr.Total))
	case !m.in.Today.IsZero() && m.in.Start.Before(m.in.Today) && len(m.stretches()) > 0:
		badge = on(th.bg, &th.amber, " waiting for review ")
	}
	brand := " " + bold(th.text, "sift") + " " + fg(th.focus, "▰") + "   " + day + "   " + badge
	return spread(brand, fg(th.muted, right), m.w)
}

// sentLine says where a confirmed worklog stands.
func (m Model) sentLine(t string) string {
	th := m.theme_()
	st, ok := m.in.Sent[t]
	switch {
	case !ok:
		return fg(th.dim, "confirmed")
	case st.State == "synced":
		return fg(th.muted, "sent to "+m.in.Tracker)
	case st.State == "local":
		return fg(th.muted, "logged locally")
	case st.State == "failed":
		return fg(th.amber, "! not sent yet") + "\n" + fg(th.muted, st.Error)
	}
	return fg(th.amber, "waiting to send · sift sync")
}

func (m Model) View() tea.View {
	th := m.theme_()
	var out []string
	switch {
	case m.w < 96 || m.h < 30:
		out = []string{fmt.Sprintf("sift needs a terminal of at least 96×30 (now %d×%d).", m.w, m.h)}
	case m.err != nil && m.in.Day.Start.IsZero():
		out = []string{"sift: " + m.err.Error()}
	default:
		out = append(out, m.header())
		evRows, sep, shown := m.laneLayout()
		topH := dayHeight(shown, evRows, sep, m.labelled())
		if shown < len(m.tickets) {
			topH++
		}
		if len(m.stretches()) == 0 {
			topH = 7
		}
		out = append(out, th.box(1, "day", fg(th.dim, "evidence and counted time"), false, m.w, topH, m.dayBox(m.w))...)
		botH := m.h - topH - 2
		rw := max(34, min(46, m.w*36/100))
		_, picking := m.mode.(choosing)
		if _, ok := m.mode.(typing); ok {
			picking = true
		}
		if m.inspect > 0 && !picking {
			out = append(out, m.inspectBox(m.w, botH)...)
		} else {
			l := m.stretchBox(m.w-rw, botH)
			if picking {
				l = m.retagBox(m.w-rw, botH)
			}
			r := m.draftBox(rw, botH)
			for i := range l {
				out = append(out, l[i]+r[i])
			}
		}
		key := func(k, what string) string { return fg(th.text, k) + fg(th.dim, " "+what+"   ") }
		status := fg(th.faint, m.in.Meetings+" ")
		switch {
		case m.msg != "":
			status = fg(th.amber, m.msg+" ")
		case m.err != nil:
			status = "could not load: " + strings.TrimSuffix(brief(m.err), ".")
			if !m.in.Now.IsZero() {
				status = "as of " + m.clockT(m.in.Now) + " · " + status
			}
			status = fg(th.amber, status+" ")
		}
		// x skips a meeting and r retags work, so the slot shows the one that applies
		act := key("r", "retag")
		if r, _ := m.selectedRow(); r.Kind == derive.InMeet {
			act = key("x", "skip")
		}
		keys := " " + key("↑↓", "select") + key("e", "inspect") + key("t", "assign") + act + key("+ −", "adjust") + key("c", "confirm") + key("[ ]", "day") + key("T", th.Name) + key("q", "quit")
		if m.dr.Confirmed {
			keys = " " + key("↑↓", "select") + key("e", "inspect") + key("[ ]", "day") + key("T", th.Name) + key("q", "quit") + fg(th.muted, "confirmed, read-only   ")
		}
		if lipgloss.Width(keys)+lipgloss.Width(status) > m.w && !m.dr.Confirmed {
			keys = " " + key("↑↓", "select") + key("e", "inspect") + key("t", "assign") + act + key("+−", "adjust") + key("c", "confirm") + key("[]", "day") + key("q", "quit")
		}
		if lipgloss.Width(keys)+lipgloss.Width(status) > m.w && m.msg == "" && m.err == nil {
			status = ""
		}
		switch m.mode.(type) {
		case nil:
		case confirming:
			keys = " " + bold(th.text, fmt.Sprintf("Confirm %s: %s, %s to %s?", m.in.Start.Format("Mon 2 Jan"), plural(len(m.dr.Sendable()), "worklog"), dur(m.dr.Total), m.src.Tracker())) +
				fg(th.dim, "   y confirms · any other key cancels")
			status = ""
		default:
			var room bool
			keys, room = m.retagKeys(key)
			if !room || m.msg == "" {
				status = ""
			}
		}
		out = append(out, spread(keys, status, m.w))
	}
	v := tea.NewView(strings.Join(out, "\n"))
	v.AltScreen = true
	v.BackgroundColor = lipgloss.Color(th.bg.String())
	v.ForegroundColor = lipgloss.Color(th.text.String())
	return v
}

// brief names a failed file by its base name: the full path would push
// the rest of the status line off the screen.
func brief(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Op + " " + filepath.Base(pe.Path) + ": " + pe.Err.Error()
	}
	return err.Error()
}
