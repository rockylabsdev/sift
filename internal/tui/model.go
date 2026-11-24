// Package tui is sift's review screen: the day as lanes of event density
// over counted time, the stretches as a table, and the draft that would be
// sent. Every change is a decision written to the day's store at once.
package tui

import (
	"regexp"
	"slices"
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
	Meetings  string            // where meetings came from, for the screen to say
	Pending   bool              // the calendar is still downloading; Refresh fetches it
	Today     time.Time         // midnight today, to say how far back the day is
	Tracker   string            // where confirmed worklogs go: "Jira", or "" for the local log
	Sent      map[string]Status // a confirmed day's worklogs, by ticket, as the outbox has them
	Now       time.Time         // the clock when the day was loaded; zero means not live
	Retags    []evidence.Retag  // the retags read into this day's sessions, in the order made
	Skipped   []derive.Meeting  // meetings the user skipped, which Day leaves out
	// Before is the day up to the latest watermark, derived as it is now:
	// what was sent, drawn grey. Day holds only the time after it.
	Before derive.Day
}

// Status is where one confirmed worklog stands.
type Status struct {
	State string // pending, failed, synced, local
	Error string
}

// Source loads days and records decisions.
type Source interface {
	Load(day time.Time) (Input, error)
	Update(day time.Time, change func(*draft.Decisions) error) error
	// Confirm accepts the day and sends its worklogs, saying what happened.
	Confirm(day time.Time) (string, error)
	// Tracker names where confirmed worklogs go ("Jira", or "the local log").
	Tracker() string
	// Refresh brings day's slow sources, such as the calendar, up to date,
	// fetching them when the Input was Pending. It runs off the UI
	// goroutine, concurrently with Load.
	Refresh(day time.Time)
	// Preview loads day as it would be after c, and saves nothing.
	Preview(day time.Time, c RetagChange) (Input, error)
	// Retag saves c.
	Retag(c RetagChange) error
	// Recent lists the tickets seen in the two weeks up to day, newest first.
	Recent(day time.Time) []string
}

// RetagChange is a retag to make, or, with Undo, one to take back.
type RetagChange struct {
	Retag evidence.Retag
	Undo  bool
}

// refresh is how often the screen reloads the day it shows.
const refresh = 10 * time.Second

type tick struct{}

type refreshed struct{}

// arrived is a Pending calendar fetched.
type arrived struct{}

func next() tea.Cmd { return tea.Tick(refresh, func(time.Time) tea.Msg { return tick{} }) }

// Model is the review screen's state.
type Model struct {
	src     Source
	in      Input
	dr      draft.Draft
	tickets []string // lane order: the draft's tickets, by first counted minute
	effT    []string // per minute of the day: the ticket credited, after decisions
	effK    []derive.Kind
	effA    []bool // per minute: counted from agent activity
	list    []row  // the stretches list: the day's stretches and its skipped meetings, by time
	cur     int
	inspect int // 0 closed; 1 the stretch in context; 2 also every event
	theme   int
	w, h    int
	msg     string
	err     error
	mode    mode // the question the keys answer; nil while browsing the day
}

// mode is a question in progress. Each one owns its keys, its footer and
// the state it needs; the live refresh waits until none is open.
type mode interface{ isMode() }

// confirming waits for y to confirm the day.
type confirming struct{}

func (confirming) isMode() {}

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
	opening := !m.in.Start.Equal(in.Start)
	m.in, m.err = in, nil
	m.recompute()
	m.cur = min(m.cur, max(len(m.list)-1, 0))
	// a day opens on what is left to review, past the sent row
	if opening && len(m.list) > 1 && m.list[0].sent {
		m.cur = 1
	}
}

// row is one line of the stretches list: a stretch of the day; a meeting
// the user skipped, kept at its time so x can count it again (its Stretch
// has its time, title and MeetingID, and claims no minutes); or, first,
// everything already sent, folded into one locked row.
type row struct {
	derive.Stretch
	skipped bool
	sent    bool
}

func rowsOf(in Input) []row {
	var out []row
	if t := in.Decisions.Through(); !t.IsZero() {
		from, to := in.Start, t
		if s := in.Before.Stretches; len(s) > 0 {
			from, to = s[0].From, s[len(s)-1].To
		}
		out = append(out, row{Stretch: derive.Stretch{From: from, To: to}, sent: true})
	}
	for _, s := range in.Day.Stretches {
		out = append(out, row{Stretch: s})
	}
	for _, mt := range in.Skipped {
		if !mt.To.After(in.Decisions.Through()) && !in.Decisions.Through().IsZero() {
			continue // wholly sent: it is part of the sent row
		}
		out = append(out, row{Stretch: derive.Stretch{From: mt.From, To: mt.To, Kind: derive.InMeet, Meeting: mt.Title, MeetingID: mt.ID}, skipped: true})
	}
	slices.SortStableFunc(out, func(a, b row) int {
		switch {
		case a.sent:
			return -1
		case b.sent:
			return 1
		}
		return a.From.Compare(b.From)
	})
	return out
}

// recompute derives everything the screen shows from in.
func (m *Model) recompute() {
	m.dr = draft.Of(m.in.Day, m.in.Decisions, m.in.Round)
	m.list = rowsOf(m.in)
	m.tickets = m.tickets[:0]
	for _, w := range append(append([]draft.Worklog{}, m.dr.Sent...), m.dr.Worklogs...) {
		if !slices.Contains(m.tickets, w.Ticket) {
			m.tickets = append(m.tickets, w.Ticket)
		}
	}
	n := m.dayLen()
	m.effT, m.effK, m.effA = make([]string, n), make([]derive.Kind, n), make([]bool, n)
	assigned := func(at time.Time) string {
		for _, s := range append(append([]derive.Stretch{}, m.in.Before.Stretches...), m.in.Day.Stretches...) {
			if !at.Before(s.From) && at.Before(s.To) {
				return m.in.Decisions.Assign[draft.ID(s)]
			}
		}
		return ""
	}
	for _, r := range append(append([]derive.Run{}, m.in.Before.Runs...), m.in.Day.Runs...) {
		t := r.Ticket
		if t == "" {
			t = assigned(r.From)
		}
		for i := m.minute(r.From); i < m.minute(r.To); i++ {
			m.effT[i], m.effK[i], m.effA[i] = t, r.Kind, r.Agent
		}
	}
}

// live is whether the clock is inside the day shown, so it is still filling in.
func (m Model) live() bool {
	return !m.in.Now.IsZero() && !m.in.Now.Before(m.in.Day.Start) && m.in.Now.Before(m.in.Day.End)
}

func (m Model) dayLen() int { return int(m.in.Day.End.Sub(m.in.Day.Start) / time.Minute) }

func (m Model) minute(t time.Time) int {
	return max(0, min(int(t.Sub(m.in.Day.Start)/time.Minute), m.dayLen()))
}

func (m Model) theme_() *Theme { return &Themes[m.theme] }

func (m Model) stretches() []derive.Stretch { return m.in.Day.Stretches }

// selected is the stretch under the cursor; a skipped meeting and the sent
// row are not ones.
func (m Model) selected() (derive.Stretch, bool) {
	if r, ok := m.selectedRow(); ok && !r.skipped && !r.sent {
		return r.Stretch, true
	}
	return derive.Stretch{}, false
}

func (m Model) selectedRow() (row, bool) {
	if m.cur < len(m.list) {
		return m.list[m.cur], true
	}
	return row{}, false
}

// skip skips the selected meeting, or counts a skipped one again, and
// keeps the cursor on it.
func (m *Model) skip() {
	r, ok := m.selectedRow()
	if !ok || r.Kind != derive.InMeet {
		m.msg = "Only meetings can be skipped."
		return
	}
	id := r.MeetingID
	m.change(func(d *draft.Decisions) error {
		if i := slices.Index(d.Skip, id); i >= 0 {
			d.Skip = slices.Delete(d.Skip, i, i+1)
		} else {
			d.Skip = append(d.Skip, id)
		}
		return nil
	})
	for i, r := range m.list {
		if r.MeetingID == id {
			m.cur = i
			break
		}
	}
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
	if err != nil {
		m.msg = err.Error()
		return
	}
	m.load(m.in.Start)
}

func (m Model) Init() tea.Cmd { return tea.Batch(next(), m.awaitCalendar()) }

// awaitCalendar fetches the calendar off the UI goroutine when the day
// shown is waiting for it.
func (m Model) awaitCalendar() tea.Cmd {
	if !m.in.Pending {
		return nil
	}
	src, day := m.src, m.in.Start
	return func() tea.Msg { src.Refresh(day); return arrived{} }
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch k := msg.(type) {
	case tick:
		src, day := m.src, m.in.Start
		return m, func() tea.Msg { src.Refresh(day); return refreshed{} }
	case refreshed:
		if m.mode == nil {
			m.load(m.in.Start)
		}
		return m, next()
	case arrived:
		if m.mode == nil {
			m.load(m.in.Start)
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.w, m.h = k.Width, k.Height
	case tea.KeyPressMsg:
		m.msg = ""
		if k.String() == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.mode.(type) {
		case nil:
			return m.browse(k)
		case confirming:
			m.mode = nil
			if k.String() != "y" {
				m.msg = "Not confirmed."
				break
			}
			msg, err := m.src.Confirm(m.in.Start)
			m.msg = msg
			if err != nil {
				m.msg = err.Error()
			}
			m.load(m.in.Start)
		default:
			return m.answerRetag(k), nil
		}
	}
	return m, nil
}

// browse answers a key while no question is open.
func (m Model) browse(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s, ok := m.selected()
	r, _ := m.selectedRow()
	switch changes := slices.Contains([]string{"t", "enter", "+", "=", "-", "r", "x"}, k.String()); {
	case r.sent && changes:
		m.msg = draft.SentError{Through: m.in.Decisions.Through().In(m.in.Start.Location())}.Error()
		return m, nil
	case r.skipped && changes && k.String() != "x":
		m.msg = "You skipped this meeting, so it has no time. x counts it again."
		return m, nil
	}
	switch k.String() {
	case "x":
		m.skip()
	case "c":
		switch {
		case m.in.Pending:
			m.msg = "The calendar is still loading. Confirm once its meetings are in."
		case len(m.dr.Sendable()) == 0 && m.dr.State == draft.Sent:
			m.msg = "Nothing new to send."
		case len(m.dr.Sendable()) == 0:
			m.msg = "Nothing to send on this day."
		default:
			m.mode = confirming{}
		}
	case "q":
		return m, tea.Quit
	case "r":
		return m.startRetag(), nil
	case "up", "k":
		m.cur = max(m.cur-1, 0)
	case "down", "j":
		m.cur = min(m.cur+1, max(len(m.list)-1, 0))
	case "e":
		m.inspect = (m.inspect + 1) % 3
	case "esc":
		m.inspect = 0
	case "T":
		m.theme = (m.theme + 1) % len(Themes)
	case "[", "left", "h":
		m.cur = 0
		m.load(m.in.Start.AddDate(0, 0, -1))
		return m, m.awaitCalendar()
	case "]", "right", "l":
		m.cur = 0
		m.load(m.in.Start.AddDate(0, 0, 1))
		return m, m.awaitCalendar()
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
	return m, nil
}
