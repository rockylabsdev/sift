package tui

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rockylabsdev/sift/internal/derive"
	"github.com/rockylabsdev/sift/internal/draft"
	"github.com/rockylabsdev/sift/internal/evidence"
)

// A retag moves one conversation's time on the ticket the screen shows to
// another ticket. It goes: choosing (when the stretch holds more than one
// conversation), typing the ticket, checking the minutes it moves, then
// saved. Time a retag already moved goes to undoing instead of typing.

// conv is one conversation's time on one ticket in the selected stretch:
// what a retag moves.
type conv struct {
	session, ticket, folder string          // ticket "" is time with no ticket
	first, last             time.Time       // the conversation's prompts on the day
	retag                   *evidence.Retag // the retag that moved this time here, if one did
}

type choosing struct {
	convs []conv
	i     int
	from  time.Time // the stretch's start
}

type typing struct {
	conv     conv
	from     time.Time // the stretch's start, the scope ctrl+f offers
	fromHere bool
	typed    string // what was typed; the suggestions are filtered by it
	pick     int    // the suggestion in the field, or -1 for what was typed
	sugg     []string
}

type checking struct {
	conv  conv
	rt    evidence.Retag
	moved int
}

type undoing struct {
	conv  conv
	rt    evidence.Retag
	moved int
}

func (choosing) isMode() {}
func (typing) isMode()   {}
func (checking) isMode() {}
func (undoing) isMode()  {}

var keyIs = regexp.MustCompile(`^` + keyRe.String() + `$`)

func (t typing) shown() []string {
	var out []string
	for _, s := range t.sugg {
		if strings.HasPrefix(s, t.typed) {
			out = append(out, s)
		}
	}
	return out
}

func (t typing) field() string {
	if s := t.shown(); t.pick >= 0 && t.pick < len(s) {
		return s[t.pick]
	}
	return t.typed
}

// convs groups the stretch's prompts by conversation and the ticket they
// count for, in the order they began. A prompt with no session cannot be
// retagged: a retag names its session.
func (m Model) convs(s derive.Stretch) (out []conv, sessionless int) {
	at := map[[2]string]int{}
	for _, e := range m.in.Events {
		if !e.Origin.IsEvidence() || e.At.Before(s.From) || !e.At.Before(s.To) {
			continue
		}
		if e.Session == "" {
			sessionless++
			continue
		}
		t := ""
		if e.Ticket != nil {
			t = *e.Ticket
		}
		k := [2]string{e.Session, t}
		i, ok := at[k]
		if !ok {
			i, at[k] = len(out), len(out)
			out = append(out, m.conversation(e.Session, t))
		}
		if e.Found != nil && e.Found.Via == "retag" && out[i].retag == nil {
			out[i].retag = m.retagOf(e)
		}
	}
	return out, sessionless
}

func (m Model) conversation(session, ticket string) conv {
	c := conv{session: session, ticket: ticket}
	for _, e := range m.in.Events {
		if e.Session != session || !e.Origin.IsEvidence() {
			continue
		}
		if c.first.IsZero() {
			c.first, c.folder = e.At, filepath.Base(e.Dir)
		}
		c.last = e.At
	}
	if c.folder == "." || c.folder == "/" {
		c.folder = ""
	}
	return c
}

// retagOf finds the retag that last moved e: the latest of the day's
// retags that would.
func (m Model) retagOf(e evidence.Event) *evidence.Retag {
	for i := len(m.in.Retags) - 1; i >= 0; i-- {
		rt := m.in.Retags[i]
		if rt.Session == e.Session && rt.Ticket == *e.Ticket && noTicket(rt.Was) == e.Found.Value && (rt.From.IsZero() || !e.At.Before(rt.From)) {
			return &rt
		}
	}
	return nil
}

func noTicket(t string) string {
	if t == "" {
		return "no ticket"
	}
	return t
}

func (m Model) convLabel(c conv) string {
	return noTicket(c.ticket) + " · " + c.where() + " · " + m.clockT(c.first) + "–" + m.clockT(c.last)
}

func (c conv) where() string {
	if c.folder == "" {
		return "a conversation"
	}
	return "conversation in " + c.folder
}

// startRetag opens a retag on the selected stretch.
func (m Model) startRetag() Model {
	s, ok := m.selected()
	switch {
	case !ok:
		m.msg = "Nothing to retag on this day."
	case s.Kind != derive.Work:
		m.msg = "Retag is for logged work; t assigns meetings and gaps."
	}
	if m.msg != "" {
		return m
	}
	cs, sessionless := m.convs(s)
	switch {
	case len(cs) == 0 && sessionless > 0:
		m.msg = "These prompts came with no conversation id, so they cannot be retagged."
	case len(cs) == 0:
		m.msg = "No prompts of yours in this stretch to retag."
	case len(cs) == 1:
		return m.chose(cs[0], s.From)
	default:
		m.mode = choosing{convs: cs, from: s.From}
	}
	return m
}

func (m Model) chose(c conv, from time.Time) Model {
	if c.retag != nil {
		moved, err := m.moved(c.retag.Ticket, RetagChange{Retag: *c.retag, Undo: true})
		if err != nil {
			m.mode, m.msg = nil, err.Error()
			return m
		}
		m.mode = undoing{conv: c, rt: *c.retag, moved: moved}
		return m
	}
	var sugg []string
	seen := map[string]bool{c.ticket: true, "": true}
	for _, t := range append(append([]string{}, m.tickets...), m.src.Recent(m.in.Start)...) {
		if !seen[t] {
			seen[t] = true
			sugg = append(sugg, t)
		}
	}
	m.mode = typing{conv: c, from: from, pick: -1, sugg: sugg}
	return m
}

// moved is how many minutes ticket t's draft loses on the day if c is
// saved; t "" is the time with no ticket.
func (m Model) moved(t string, c RetagChange) (int, error) {
	before, err := m.src.Load(m.in.Start)
	if err != nil {
		return 0, err
	}
	after, err := m.src.Preview(m.in.Start, c)
	if err != nil {
		return 0, err
	}
	return minutesOf(before, t) - minutesOf(after, t), nil
}

func minutesOf(in Input, t string) int {
	d := draft.Of(in.Day, in.Decisions, in.Round)
	if t == "" {
		return d.Unassigned.Ticketless
	}
	for _, w := range d.Worklogs {
		if w.Ticket == t {
			return w.Minutes
		}
	}
	return 0
}

func (m Model) answerRetag(k tea.KeyPressMsg) Model {
	switch md := m.mode.(type) {
	case choosing:
		switch k.String() {
		case "up", "k":
			md.i = max(md.i-1, 0)
		case "down", "j":
			md.i = min(md.i+1, len(md.convs)-1)
		case "enter":
			return m.chose(md.convs[md.i], md.from)
		case "esc":
			m.mode, m.msg = nil, "Not retagged."
			return m
		}
		m.mode = md
	case typing:
		n := len(md.shown())
		switch k.String() {
		case "esc":
			m.mode, m.msg = nil, "Not retagged."
			return m
		case "enter":
			return m.check(md)
		case "tab", "down":
			if n > 0 {
				md.pick = (md.pick+2)%(n+1) - 1
			}
		case "shift+tab", "up":
			if n > 0 {
				md.pick = (md.pick+n+1)%(n+1) - 1
			}
		case "backspace":
			f := md.field()
			md.typed, md.pick = f[:max(len(f)-1, 0)], -1
		case "ctrl+f":
			md.fromHere = !md.fromHere
		default:
			if c := strings.ToUpper(k.Text); len(c) == 1 && strings.Contains("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-", c) {
				md.typed, md.pick = md.field()+c, -1
			}
		}
		m.mode = md
	case checking:
		m.mode = nil
		if k.String() != "y" {
			m.msg = "Not retagged."
			return m
		}
		if err := m.src.Retag(RetagChange{Retag: md.rt}); err != nil {
			m.msg = err.Error()
			return m
		}
		m.load(m.in.Start)
		m.msg = fmt.Sprintf("Moved %s to %s.", dur(md.moved), md.rt.Ticket)
	case undoing:
		m.mode = nil
		if k.String() != "y" {
			m.msg = "Not put back."
			return m
		}
		if err := m.src.Retag(RetagChange{Retag: md.rt, Undo: true}); err != nil {
			m.msg = err.Error()
			return m
		}
		m.load(m.in.Start)
		m.msg = fmt.Sprintf("Put back %s to %s.", dur(md.moved), noTicket(md.rt.Was))
	}
	return m
}

func (m Model) check(t typing) Model {
	key := t.field()
	switch {
	case !keyIs.MatchString(key):
		m.msg = "A ticket key looks like PUN-160."
		return m
	case key == t.conv.ticket:
		m.msg = "This time is already on " + key + "."
		return m
	}
	rt := evidence.Retag{Session: t.conv.session, Was: t.conv.ticket, Ticket: key}
	if t.fromHere {
		rt.From = t.from
	}
	moved, err := m.moved(t.conv.ticket, RetagChange{Retag: rt})
	if err != nil {
		m.msg = err.Error()
		return m
	}
	m.mode = checking{conv: t.conv, rt: rt, moved: moved}
	return m
}

// movedFrom says where the stretch's retagged prompts were: the one ticket
// they came from, "moved" for several, "" when none were retagged.
func (m Model) movedFrom(s derive.Stretch) string {
	from := map[string]bool{}
	for _, e := range m.in.Events {
		if e.Origin.IsEvidence() && e.Found != nil && e.Found.Via == "retag" && !e.At.Before(s.From) && e.At.Before(s.To) {
			from[e.Found.Value] = true
		}
	}
	switch len(from) {
	case 0:
		return ""
	case 1:
		return keysOf(from)[0]
	}
	return "moved"
}

// --- view ---

func (m Model) retagBox(w, h int) []string {
	th := m.theme_()
	iw := w - 2
	var body []string
	right := ""
	switch md := m.mode.(type) {
	case choosing:
		body = append(body, " "+fg(th.text, "Which conversation's time?")+fg(th.dim, " "+plural(len(md.convs), "conversation")+" in this stretch"), "")
		for i, c := range md.convs {
			line := m.convLabel(c)
			if c.retag != nil {
				line += " · moved from " + noTicket(c.retag.Was)
			}
			if i == md.i {
				body = append(body, on(th.text, &th.sel, pad(bold(th.text, "›")+fg(th.text, line), iw)))
			} else {
				body = append(body, " "+fg(th.muted, line))
			}
		}
		right = fg(th.dim, fmt.Sprintf("%d of %d", md.i+1, len(md.convs)))
	case typing:
		body = append(body, " "+fg(th.text, m.convLabel(md.conv)), "")
		scope := "the whole conversation"
		if md.fromHere {
			scope = "its time from " + m.clockT(md.from) + " on"
		}
		f := md.field()
		field := on(th.text, &th.sel, " "+f) + on(th.bg, &th.text, " ") + on(th.text, &th.sel, strings.Repeat(" ", max(12-len(f), 0)))
		body = append(body, " "+fg(th.muted, "count it for ")+field+fg(th.muted, "   moving ")+fg(th.text, scope), "")
		shown := md.shown()
		day := map[string]bool{}
		for _, t := range m.tickets {
			day[t] = true
		}
		room := h - 2 - len(body)
		for i, t := range shown {
			if i == room-1 && len(shown) > room {
				body = append(body, "  "+fg(th.dim, fmt.Sprintf("+ %d more", len(shown)-i)))
				break
			}
			where := "seen lately"
			if day[t] {
				where = "on this day"
			}
			line := pad(t, 12) + fg(th.dim, where)
			if i == md.pick {
				body = append(body, on(th.text, &th.sel, pad(bold(th.text, "› ")+pad(bold(m.hue(t), t), 12)+fg(th.dim, where), iw)))
			} else {
				body = append(body, "  "+fg(th.muted, line))
			}
		}
		if len(shown) == 0 && md.typed != "" {
			body = append(body, "  "+fg(th.dim, "no ticket seen lately starts "+md.typed+"; enter takes it as typed"))
		}
		right = fg(th.dim, "moves this conversation's "+noTicket(md.conv.ticket)+" time")
	}
	return th.box(2, "retag", right, true, w, h, body)
}

// retagKeys is the footer while a retag is open, and whether the status
// line has room beside it.
func (m Model) retagKeys(key func(k, what string) string) (string, bool) {
	th := m.theme_()
	ask := func(q, hint string) string {
		line := " " + bold(th.text, q) + fg(th.dim, "   "+hint+" · any other key cancels")
		if lipgloss.Width(line) > m.w {
			line = " " + bold(th.text, q) + fg(th.dim, "  "+hint)
		}
		return line
	}
	switch md := m.mode.(type) {
	case choosing:
		return " " + key("↑↓", "choose") + key("enter", "pick") + key("esc", "cancel"), true
	case typing:
		return " " + key("tab ↑↓", "suggestions") + key("ctrl+f", "scope") + key("enter", "check") + key("esc", "cancel"), true
	case checking:
		of := md.conv.ticket
		if of == "" {
			of = "time with no ticket"
		}
		return ask(fmt.Sprintf("Move %s of %s (%s) to %s?", dur(md.moved), of, md.conv.where(), md.rt.Ticket), "y saves"), false
	case undoing:
		to := "to " + md.rt.Was
		if md.rt.Was == "" {
			to = "as time with no ticket"
		}
		return ask(fmt.Sprintf("Put back %s of %s %s?", dur(md.moved), md.rt.Ticket, to), "y puts back"), false
	}
	return "", true
}
