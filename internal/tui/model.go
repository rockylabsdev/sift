// Package tui is sift's review screen: the day as lanes of event density
// over counted time, the stretches as a table, and the draft that would be
// sent. Every change is a decision written to the day's store at once.
package tui

import (
	"errors"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rockylabsdev/sift/internal/derive"
	"github.com/rockylabsdev/sift/internal/draft"
	"github.com/rockylabsdev/sift/internal/evidence"
)

// Input is one day as the screen needs it.
type Input struct {
	Start     time.Time
	Events    []evidence.Event // the day's own events
	Day       derive.Day
	Decisions draft.Decisions
	Round     int
	IdleGap   int
	Meetings  string // where meetings came from, for the screen to say
}

// Source loads days and records decisions.
type Source interface {
	Load(day time.Time) (Input, error)
	Update(day time.Time, change func(*draft.Decisions) error) error
	// Confirm accepts the day and sends its worklogs, saying what happened.
	Confirm(day time.Time) (string, error)
	// Tracker names where confirmed worklogs go ("Jira", or "the local log").
	Tracker() string
}

// Model is the review screen's state.
type Model struct {
	src     Source
	in      Input
	dr      draft.Draft
	tickets []string // lane order: the draft's tickets, by first counted minute
	effT    []string // per minute of the day: the ticket credited, after decisions
	effK    []derive.Kind
	cur     int
	inspect int // 0 closed; 1 the stretch in context; 2 also every event
	theme   int
	w, h    int
	msg     string
	err     error
	asking  bool // waiting for y to confirm the day
}

// New opens the review on day.
func New(src Source, day time.Time, theme string) Model {
	m := Model{src: src, theme: ThemeNamed(theme), w: 120, h: 36}
	m.load(day)
	return m
}

func (m *Model) load(day time.Time) {
	in, err := m.src.Load(day)
	if err != nil {
		m.err = err
		return
	}
	m.in, m.err = in, nil
	m.cur = min(m.cur, max(len(in.Day.Stretches)-1, 0))
	m.recompute()
}

// recompute derives everything the screen shows from in.
func (m *Model) recompute() {
	m.dr = draft.Of(m.in.Day, m.in.Decisions, m.in.Round)
	m.tickets = m.tickets[:0]
	for _, w := range m.dr.Worklogs {
		m.tickets = append(m.tickets, w.Ticket)
	}
	n := m.dayLen()
	m.effT, m.effK = make([]string, n), make([]derive.Kind, n)
	assigned := func(at time.Time) string {
		for _, s := range m.in.Day.Stretches {
			if !at.Before(s.From) && at.Before(s.To) {
				return m.in.Decisions.Assign[draft.ID(s)]
			}
		}
		return ""
	}
	for _, r := range m.in.Day.Runs {
		t := r.Ticket
		if t == "" {
			t = assigned(r.From)
		}
		for i := m.minute(r.From); i < m.minute(r.To); i++ {
			m.effT[i], m.effK[i] = t, r.Kind
		}
	}
}

func (m Model) dayLen() int { return int(m.in.Day.End.Sub(m.in.Day.Start) / time.Minute) }

func (m Model) minute(t time.Time) int {
	return max(0, min(int(t.Sub(m.in.Day.Start)/time.Minute), m.dayLen()))
}

func (m Model) theme_() *Theme { return &Themes[m.theme] }

func (m Model) stretches() []derive.Stretch { return m.in.Day.Stretches }

func (m Model) selected() (derive.Stretch, bool) {
	s := m.stretches()
	if m.cur < len(s) {
		return s[m.cur], true
	}
	return derive.Stretch{}, false
}

func (m Model) worklog(t string) draft.Worklog {
	for _, w := range m.dr.Worklogs {
		if w.Ticket == t {
			return w
		}
	}
	return draft.Worklog{Ticket: t}
}

var keyRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]+-[0-9]+`)

// suggestion is the ticket a meeting's title names, if any.
func suggestion(s derive.Stretch) string {
	if s.Kind != derive.InMeet {
		return ""
	}
	return strings.ToUpper(keyRe.FindString(s.Meeting))
}

// options is the order t cycles an assignable stretch through: the ticket
// its title names first, then the day's tickets, then unassigned.
func (m Model) options(s derive.Stretch) []string {
	var out []string
	seen := map[string]bool{}
	add := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	add(suggestion(s))
	for _, t := range m.tickets {
		add(t)
	}
	return append(out, "")
}

// target is the ticket + and - change on a stretch: the one assigned to
// it, else the one with the most minutes in it.
func (m Model) target(s derive.Stretch) string {
	if t := m.in.Decisions.Assign[draft.ID(s)]; t != "" {
		return t
	}
	best, most := "", 0
	for t, n := range s.Tickets {
		if n > most || (n == most && t < best) {
			best, most = t, n
		}
	}
	return best
}

func (m *Model) change(f func(*draft.Decisions) error) {
	err := m.src.Update(m.in.Start, f)
	if errors.Is(err, draft.ErrConfirmed) {
		m.msg = "This day is confirmed and can no longer change."
		return
	}
	if err != nil {
		m.msg = err.Error()
		return
	}
	m.load(m.in.Start)
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch k := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = k.Width, k.Height
	case tea.KeyPressMsg:
		m.msg = ""
		if m.asking {
			m.asking = false
			if k.String() == "y" {
				msg, err := m.src.Confirm(m.in.Start)
				m.msg = msg
				if err != nil {
					m.msg = err.Error()
				}
				m.load(m.in.Start)
			} else {
				m.msg = "Not confirmed."
			}
			return m, nil
		}
		s, ok := m.selected()
		switch k.String() {
		case "c":
			switch {
			case m.dr.Confirmed:
				m.msg = "Already confirmed."
			case len(m.dr.Sendable()) == 0:
				m.msg = "Nothing to confirm on this day."
			default:
				m.asking = true
			}
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.cur = max(m.cur-1, 0)
		case "down", "j":
			m.cur = min(m.cur+1, max(len(m.stretches())-1, 0))
		case "e":
			m.inspect = (m.inspect + 1) % 3
		case "esc":
			m.inspect = 0
		case "T":
			m.theme = (m.theme + 1) % len(Themes)
		case "[", "left", "h":
			m.cur = 0
			m.load(m.in.Start.AddDate(0, 0, -1))
		case "]", "right", "l":
			m.cur = 0
			m.load(m.in.Start.AddDate(0, 0, 1))
		case "t", "enter":
			if _, can := draft.Assignable(s); ok && can {
				id, opts := draft.ID(s), m.options(s)
				cur := m.in.Decisions.Assign[id]
				next := opts[0]
				for i, o := range opts {
					if o == cur {
						next = opts[(i+1)%len(opts)]
					}
				}
				m.change(func(d *draft.Decisions) error {
					if d.Assign == nil {
						d.Assign = map[string]string{}
					}
					if next == "" {
						delete(d.Assign, id)
					} else {
						d.Assign[id] = next
					}
					return nil
				})
			} else if ok {
				m.msg = "Only meetings, gaps and ticketless time can be assigned."
			}
		case "+", "=", "-":
			t := m.target(s)
			if !ok || t == "" {
				m.msg = "Select a stretch with a ticket to adjust it."
				break
			}
			w := m.worklog(t)
			base := w.Counted + w.Assigned
			m.change(func(d *draft.Decisions) error {
				if d.Adjust == nil {
					d.Adjust = map[string]int{}
				}
				if k.String() == "-" {
					d.Adjust[t] = max(d.Adjust[t]-5, -base)
				} else {
					d.Adjust[t] += 5
				}
				if d.Adjust[t] == 0 {
					delete(d.Adjust, t)
				}
				return nil
			})
		}
	}
	return m, nil
}
