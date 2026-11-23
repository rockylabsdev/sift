// Package draft owns a day's decisions (what the user adjusted, assigned
// and confirmed) and turns a derived day plus those decisions into the
// worklogs it would send. The CLI and the review screen both read drafts
// from here, so a number means the same thing everywhere.
package draft

import (
	"sort"
	"time"

	"github.com/rockylabsdev/sift/internal/derive"
)

// Decisions is everything the user decided about one day.
type Decisions struct {
	V         int               `json:"v"`
	Date      string            `json:"date"`
	Adjust    map[string]int    `json:"adjust,omitempty"` // minutes added (or removed) per ticket
	Assign    map[string]string `json:"assign,omitempty"` // stretch id → ticket, for meetings, gaps and ticketless time
	Blocks    []Block           `json:"blocks,omitempty"`
	Skip      []string          `json:"skip,omitempty"` // meetings the user was not in, by derive.Meeting ID
	Confirmed *Confirmed        `json:"confirmed,omitempty"`
}

// Block is time added to a ticket by hand, for work no collector saw.
type Block struct {
	Ticket string    `json:"ticket"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
}

// Confirmed is a day as it was accepted. It never changes afterwards, so
// a later change of settings cannot alter what was sent.
type Confirmed struct {
	At       time.Time `json:"at"`
	Worklogs []Worklog `json:"worklogs"`
}

// Worklog is one ticket's worklog for the day.
type Worklog struct {
	Ticket   string    `json:"ticket"`
	Minutes  int       `json:"minutes"`
	Counted  int       `json:"counted"`  // from evidence, before decisions
	Assigned int       `json:"assigned"` // meetings, gaps and ticketless time given to it
	ByHand   int       `json:"byHand"`   // what the adjustment changed, after the floor
	Agent    int       `json:"agent"`    // of Counted, minutes from agent activity
	Started  time.Time `json:"started"`
}

// Draft is what a day would send, with what is left unassigned.
type Draft struct {
	Date       string            `json:"date"`
	Worklogs   []Worklog         `json:"worklogs"`
	Total      int               `json:"total"`
	Unassigned derive.Unassigned `json:"unassigned"`
	Confirmed  bool              `json:"confirmed"`
}

// ID names a stretch for an assignment. It holds across re-derivations as
// long as the stretch starts at the same minute.
func ID(s derive.Stretch) string {
	return string(s.Kind) + "@" + s.From.Format("15:04")
}

// Assignable reports whether a stretch's time can be given to a ticket,
// and how many minutes that is: a meeting, a gap, or the ticketless part
// of a work stretch.
func Assignable(s derive.Stretch) (minutes int, ok bool) {
	return s.Unassigned, s.Unassigned > 0
}

// Of builds the draft for day under dec, rounding each total to round
// minutes (0, 5 or 15). A confirmed day returns what was confirmed.
func Of(day derive.Day, dec Decisions, round int) Draft {
	d := Draft{Date: day.Start.Format("2006-01-02"), Unassigned: day.Unassigned}
	if dec.Confirmed != nil {
		d.Worklogs, d.Confirmed = dec.Confirmed.Worklogs, true
		for _, w := range d.Worklogs {
			d.Total += w.Minutes
		}
		return d
	}
	by := map[string]*Worklog{}
	get := func(t string, at time.Time) *Worklog {
		w, ok := by[t]
		if !ok {
			w = &Worklog{Ticket: t, Started: at}
			by[t] = w
		}
		if at.Before(w.Started) {
			w.Started = at
		}
		return w
	}
	for _, t := range day.Totals {
		w := get(t.Ticket, t.First)
		w.Counted, w.Agent = t.Minutes, t.Agent
	}
	for _, s := range day.Stretches {
		t, ok := dec.Assign[ID(s)]
		n, can := Assignable(s)
		if !ok || !can || t == "" {
			continue
		}
		get(t, s.From).Assigned += n
		switch s.Kind {
		case derive.InMeet:
			d.Unassigned.Meetings -= n
		case derive.Silence:
			d.Unassigned.Gaps -= n
		default:
			d.Unassigned.Ticketless -= n
		}
	}
	for t, n := range dec.Adjust {
		if _, ok := by[t]; !ok && n != 0 {
			by[t] = &Worklog{Ticket: t, Started: day.Start} // an adjusted ticket with no time still shows
		}
	}
	for _, w := range by {
		base := w.Counted + w.Assigned
		w.Minutes = max(base+dec.Adjust[w.Ticket], 0)
		w.ByHand = w.Minutes - base
		w.Minutes = roundTo(w.Minutes, round)
		if w.Minutes >= 1 || w.ByHand != 0 {
			d.Worklogs = append(d.Worklogs, *w)
			d.Total += w.Minutes
		}
	}
	sort.Slice(d.Worklogs, func(i, j int) bool {
		a, b := d.Worklogs[i], d.Worklogs[j]
		if !a.Started.Equal(b.Started) {
			return a.Started.Before(b.Started)
		}
		return a.Ticket < b.Ticket
	})
	if d.Worklogs == nil {
		d.Worklogs = []Worklog{}
	}
	return d
}

func roundTo(n, to int) int {
	if to == 0 {
		return n
	}
	return (n + to/2) / to * to
}

// Sendable is the worklogs confirm would write: those of at least a minute.
func (d Draft) Sendable() []Worklog {
	var out []Worklog
	for _, w := range d.Worklogs {
		if w.Minutes >= 1 {
			out = append(out, w)
		}
	}
	return out
}
