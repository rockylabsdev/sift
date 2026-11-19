// Package derive turns one day's evidence into the work it stands for.
//
// For is a pure function: the same events, meetings and settings always
// give the same day. It reads no clock and no files; a caller that wants
// nothing counted after now passes it as Settings.Until.
package derive

import (
	"sort"
	"time"
)

// Event is one piece of evidence: when, and the ticket it was for ("" when
// the session named none).
type Event struct {
	At     time.Time
	Ticket string
	// Agent marks an agent's activity, not the user's: it only fills
	// minutes the user's own evidence, meetings and blocks leave unclaimed.
	Agent bool
	Span  time.Duration // how long the act ran, ending at At
}

// AgentMode is how agent activity counts.
type AgentMode string

const (
	AgentFill    AgentMode = "fill"    // fills any unclaimed minute (the default)
	AgentWorkday AgentMode = "workday" // only between the user's first and last evidence
	AgentOff     AgentMode = "off"
)

// Meeting is a calendar event that occupied the user.
type Meeting struct {
	From, To time.Time
	Title    string
}

// Block is time the user gave a ticket by hand, for work no collector
// saw. It owns its minutes ahead of evidence and meetings.
type Block struct {
	From, To time.Time
	Ticket   string
}

// Settings are the rules' knobs.
type Settings struct {
	IdleGap   int  // longest silence, in minutes, that still joins two events
	Tail      int  // minutes credited after a stretch's last event
	EventsWin bool // an event during a meeting beats the meeting
	Blocks    []Block
	Agent     AgentMode // "" is AgentFill
	Until     time.Time // no minute after the one it falls in is counted; zero for no limit
}

// Defaults are the rules as prototyped.
var Defaults = Settings{IdleGap: 10, Tail: 5}

// Kind is what a stretch is.
type Kind string

const (
	Work    Kind = "work"
	InMeet  Kind = "meeting"
	Silence Kind = "gap"
)

// Stretch is a run of the day with one meaning: work (split between the
// tickets whose events fall in it), one meeting, or one gap.
type Stretch struct {
	From        time.Time      `json:"from"`
	To          time.Time      `json:"to"`
	Kind        Kind           `json:"kind"`
	Tickets     map[string]int `json:"tickets,omitempty"`    // minutes counted per ticket
	Unassigned  int            `json:"unassigned,omitempty"` // minutes no ticket owns
	Agent       map[string]int `json:"agent,omitempty"`      // of those, minutes from agent activity, by ticket ("" for none)
	Meeting     string         `json:"meeting,omitempty"`
	Events      int            `json:"events"`
	AgentEvents int            `json:"agentEvents,omitempty"`
}

// Minutes is the stretch's length.
func (s Stretch) Minutes() int { return int(s.To.Sub(s.From) / time.Minute) }

// Total is the time counted for one ticket in the day.
type Total struct {
	Ticket  string    `json:"ticket"`
	Minutes int       `json:"minutes"`
	Agent   int       `json:"agent,omitempty"` // of those, minutes from agent activity
	First   time.Time `json:"first"`           // its first counted minute
}

// Unassigned is the day's time that no ticket owns yet.
type Unassigned struct {
	Meetings   int `json:"meetings"`
	Gaps       int `json:"gaps"`
	Ticketless int `json:"ticketless"`
}

// Run is a span of minutes with a single owner: one ticket, ticketless
// work, one meeting, or a gap. Runs tile the day's counted time; the
// review screen draws them minute by minute.
type Run struct {
	From, To time.Time
	Kind     Kind
	Ticket   string // a work run's ticket; "" when ticketless
	Meeting  string
	Agent    bool // from agent activity
}

// Day is a derived day.
type Day struct {
	Runs        []Run      `json:"-"`
	Start       time.Time  `json:"start"`
	End         time.Time  `json:"end"`
	Stretches   []Stretch  `json:"stretches"`
	Totals      []Total    `json:"totals"`
	Unassigned  Unassigned `json:"unassigned"`
	Events      int        `json:"events"`
	AgentEvents int        `json:"agentEvents,omitempty"`
}

// owner codes for one minute
const (
	none  = -1
	gap   = -2
	loose = -3   // an event with no ticket
	meet0 = -100 // meet0-i is meeting i
)

func isMeeting(o int) bool { return o <= meet0 }

// For derives the local calendar day that contains start. Events and
// meetings outside the day may be passed; they only matter where a
// stretch crosses midnight, and nothing outside the day is counted.
func For(start time.Time, events []Event, meetings []Meeting, s Settings) Day {
	y, mo, d := start.Date()
	start = time.Date(y, mo, d, 0, 0, 0, 0, start.Location())
	end := time.Date(y, mo, d+1, 0, 0, 0, 0, start.Location())

	pad := s.IdleGap + s.Tail
	n := int(end.Sub(start)/time.Minute) + 2*pad
	idx := func(t time.Time) int { return int(t.Sub(start).Truncate(time.Minute)/time.Minute) + pad }

	type ev struct{ i, owner, from int } // from: where an agent act began
	var keys []string
	key := map[string]int{}
	var evs []ev
	sorted := append([]Event(nil), events...)
	sort.Slice(sorted, func(a, b int) bool {
		x, y := sorted[a], sorted[b]
		switch {
		case !x.At.Equal(y.At):
			return x.At.Before(y.At)
		case x.Ticket != y.Ticket:
			return x.Ticket < y.Ticket // same instant: order must not depend on input
		case x.Agent != y.Agent:
			return !x.Agent
		}
		return x.Span < y.Span
	})
	var agents []ev
	agentOn := s.Agent != AgentOff
	for _, e := range sorted {
		i := idx(e.At)
		if i < 0 || i >= n || (e.Agent && !agentOn) {
			continue
		}
		o := loose
		if e.Ticket != "" {
			k, ok := key[e.Ticket]
			if !ok {
				k = len(keys)
				key[e.Ticket] = k
				keys = append(keys, e.Ticket)
			}
			o = k
		}
		if e.Agent {
			agents = append(agents, ev{i, o, idx(e.At.Add(-e.Span))})
			continue
		}
		evs = append(evs, ev{i, o, i})
	}

	own := make([]int, n)
	for i := range own {
		own[i] = none
	}
	fill := func(from, to, o int, onlyEmpty bool) {
		for i := max(from, 0); i < min(to, n); i++ {
			if !onlyEmpty || own[i] == none {
				own[i] = o
			}
		}
	}
	for j, e := range evs {
		if j+1 < len(evs) && evs[j+1].i-e.i <= s.IdleGap {
			next := evs[j+1]
			mid := (e.i + next.i + 1) / 2
			fill(e.i, mid, e.owner, false)
			fill(mid, next.i, next.owner, false)
			continue
		}
		stop := e.i + s.Tail
		if j+1 < len(evs) {
			stop = min(stop, evs[j+1].i)
		}
		fill(e.i, stop, e.owner, false)
	}
	for k, m := range meetings {
		fill(idx(m.From), idx(m.To), meet0-k, s.EventsWin)
	}
	for _, b := range s.Blocks {
		k, ok := key[b.Ticket]
		if !ok {
			k = len(keys)
			key[b.Ticket] = k
			keys = append(keys, b.Ticket)
		}
		fill(idx(b.From), idx(b.To), k, false)
	}
	agentMin := make([]bool, n)
	if len(agents) > 0 {
		lo, hi := 0, n
		if s.Agent == AgentWorkday {
			lo, hi = n, 0
			if len(evs) > 0 {
				lo, hi = evs[0].i, evs[len(evs)-1].i+s.Tail
			}
		}
		// each ticket's agent activity covers its acts, joined across
		// silences of at most the idle gap, with the usual tail
		var groups []int
		cover := map[int][]bool{}
		byOwner := map[int][]ev{}
		for _, a := range agents {
			if _, ok := byOwner[a.owner]; !ok {
				groups = append(groups, a.owner)
			}
			byOwner[a.owner] = append(byOwner[a.owner], a)
		}
		name := func(o int) string {
			if o == loose {
				return ""
			}
			return keys[o]
		}
		sort.Slice(groups, func(a, b int) bool { return name(groups[a]) < name(groups[b]) })
		for _, g := range groups {
			acts := byOwner[g]
			sort.Slice(acts, func(a, b int) bool { return acts[a].from < acts[b].from })
			c := make([]bool, n)
			mark := func(from, to int) {
				for i := max(from, 0); i < min(to, n); i++ {
					c[i] = true
				}
			}
			end := -1
			for j, a := range acts { // like a prompt: its span, then the tail
				mark(a.from, a.i)
				end = max(end, a.i)
				if j+1 < len(acts) && acts[j+1].from-end <= s.IdleGap {
					mark(end, acts[j+1].from)
				} else {
					mark(end, end+s.Tail)
				}
			}
			cover[g] = c
		}
		// a free minute goes to one of the tickets whose agents cover it,
		// in turn, so concurrent agents share the time and never add to it
		active := make([]int, 0, len(groups))
		for i := max(lo, 0); i < min(hi, n); i++ {
			if own[i] != none {
				continue
			}
			active = active[:0]
			for _, g := range groups {
				if cover[g][i] {
					active = append(active, g)
				}
			}
			if len(active) > 0 {
				own[i], agentMin[i] = active[i%len(active)], true
			}
		}
	}
	if len(evs)+len(agents) > 0 {
		first, last := n, -1
		for _, e := range evs {
			first, last = min(first, e.i), max(last, e.i)
		}
		if s.Agent != AgentWorkday { // in workday mode the day's span is the user's
			for _, a := range agents {
				first, last = min(first, a.from), max(last, a.i)
			}
		}
		for i := max(first, 0); i < min(last, n); i++ {
			if own[i] == none {
				own[i] = gap
			}
		}
	}

	// keep only the day, and drop silences too short to be gaps
	own, agentMin = own[pad:n-pad], agentMin[pad:n-pad]
	if !s.Until.IsZero() {
		for i := max(idx(s.Until)+1-pad, 0); i < len(own); i++ {
			own[i], agentMin[i] = none, false
		}
	}
	for i := 0; i < len(own); {
		j := i
		for j < len(own) && own[j] == own[i] {
			j++
		}
		if own[i] == gap && j-i < s.IdleGap {
			for x := i; x < j; x++ {
				own[x] = none
			}
		}
		i = j
	}

	day := Day{Start: start, End: end}
	for _, e := range evs {
		if e.i >= pad && e.i < n-pad {
			day.Events++
		}
	}
	for _, a := range agents {
		if a.i >= pad && a.i < n-pad {
			day.AgentEvents++
		}
	}
	at := func(i int) time.Time { return start.Add(time.Duration(i) * time.Minute) }
	totals := make([]Total, len(keys))
	for i, o := range own {
		switch {
		case o >= 0:
			if totals[o].Minutes == 0 {
				totals[o].First = at(i)
			}
			totals[o].Minutes++
			if agentMin[i] {
				totals[o].Agent++
			}
		case o == loose:
			day.Unassigned.Ticketless++
		case o == gap:
			day.Unassigned.Gaps++
		case isMeeting(o):
			day.Unassigned.Meetings++
		}
	}
	for k, t := range totals {
		if t.Minutes > 0 {
			t.Ticket = keys[k]
			day.Totals = append(day.Totals, t)
		}
	}
	sort.SliceStable(day.Totals, func(a, b int) bool { return day.Totals[a].First.Before(day.Totals[b].First) })

	for i := 0; i < len(own); {
		j := i
		for j < len(own) && own[j] == own[i] && agentMin[j] == agentMin[i] {
			j++
		}
		if o := own[i]; o != none {
			r := Run{From: at(i), To: at(j), Kind: Work, Agent: agentMin[i]}
			switch {
			case o >= 0:
				r.Ticket = keys[o]
			case isMeeting(o):
				r.Kind, r.Meeting = InMeet, meetings[meet0-o].Title
			case o == gap:
				r.Kind = Silence
			}
			day.Runs = append(day.Runs, r)
		}
		i = j
	}

	isWork := func(o int) bool { return o >= 0 || o == loose }
	for i := 0; i < len(own); {
		o := own[i]
		j := i
		for j < len(own) && (own[j] == o || (isWork(o) && isWork(own[j]))) {
			j++
		}
		if o != none {
			st := Stretch{From: at(i), To: at(j)}
			switch {
			case isWork(o):
				st.Kind, st.Tickets = Work, map[string]int{}
				for k, x := range own[i:j] {
					t := ""
					if x >= 0 {
						t = keys[x]
						st.Tickets[t]++
					} else {
						st.Unassigned++
					}
					if agentMin[i+k] {
						if st.Agent == nil {
							st.Agent = map[string]int{}
						}
						st.Agent[t]++
					}
				}
			case isMeeting(o):
				st.Kind, st.Meeting, st.Unassigned = InMeet, meetings[meet0-o].Title, j-i
			default:
				st.Kind, st.Unassigned = Silence, j-i
			}
			for _, e := range evs {
				if e.i-pad >= i && e.i-pad < j {
					st.Events++
				}
			}
			for _, a := range agents {
				if a.i-pad >= i && a.i-pad < j {
					st.AgentEvents++
				}
			}
			day.Stretches = append(day.Stretches, st)
		}
		i = j
	}
	return day
}
