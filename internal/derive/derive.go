// Package derive turns one day's evidence into the work it stands for.
//
// Day is a pure function: the same events, meetings and settings always
// give the same day. It reads no clock and no files.
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
}

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
	From       time.Time      `json:"from"`
	To         time.Time      `json:"to"`
	Kind       Kind           `json:"kind"`
	Tickets    map[string]int `json:"tickets,omitempty"`    // minutes counted per ticket
	Unassigned int            `json:"unassigned,omitempty"` // minutes no ticket owns
	Meeting    string         `json:"meeting,omitempty"`
	Events     int            `json:"events"`
}

// Minutes is the stretch's length.
func (s Stretch) Minutes() int { return int(s.To.Sub(s.From) / time.Minute) }

// Total is the time counted for one ticket in the day.
type Total struct {
	Ticket  string    `json:"ticket"`
	Minutes int       `json:"minutes"`
	First   time.Time `json:"first"` // its first counted minute
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
}

// Day is a derived day.
type Day struct {
	Runs       []Run      `json:"-"`
	Start      time.Time  `json:"start"`
	End        time.Time  `json:"end"`
	Stretches  []Stretch  `json:"stretches"`
	Totals     []Total    `json:"totals"`
	Unassigned Unassigned `json:"unassigned"`
	Events     int        `json:"events"`
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

	type ev struct{ i, owner int }
	var keys []string
	key := map[string]int{}
	var evs []ev
	sorted := append([]Event(nil), events...)
	sort.Slice(sorted, func(a, b int) bool {
		x, y := sorted[a], sorted[b]
		if !x.At.Equal(y.At) {
			return x.At.Before(y.At)
		}
		return x.Ticket < y.Ticket // same instant: order must not depend on input
	})
	for _, e := range sorted {
		i := idx(e.At)
		if i < 0 || i >= n {
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
		evs = append(evs, ev{i, o})
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
	if len(evs) > 0 {
		first, last := evs[0].i, evs[len(evs)-1].i
		for i := first; i < last; i++ {
			if own[i] == none {
				own[i] = gap
			}
		}
	}

	// keep only the day, and drop silences too short to be gaps
	own = own[pad : n-pad]
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
	at := func(i int) time.Time { return start.Add(time.Duration(i) * time.Minute) }
	totals := make([]Total, len(keys))
	for i, o := range own {
		switch {
		case o >= 0:
			if totals[o].Minutes == 0 {
				totals[o].First = at(i)
			}
			totals[o].Minutes++
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
		for j < len(own) && own[j] == own[i] {
			j++
		}
		if o := own[i]; o != none {
			r := Run{From: at(i), To: at(j), Kind: Work}
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
				for _, x := range own[i:j] {
					if x >= 0 {
						st.Tickets[keys[x]]++
					} else {
						st.Unassigned++
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
			day.Stretches = append(day.Stretches, st)
		}
		i = j
	}
	return day
}
