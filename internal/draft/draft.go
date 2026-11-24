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
	V      int               `json:"v"`
	Date   string            `json:"date"`
	Adjust map[string]int    `json:"adjust,omitempty"` // minutes added (or removed) per ticket
	Assign map[string]string `json:"assign,omitempty"` // stretch id → ticket, for meetings, gaps and ticketless time
	Blocks []Block           `json:"blocks,omitempty"`
	Skip   []string          `json:"skip,omitempty"` // meetings the user was not in, by derive.Meeting ID
	Sent   []Send            `json:"sent,omitempty"` // each confirm, oldest first
	// Confirmed is how sift before partial sends froze a whole day. Load
	// reads it as one send through the end of the day.
	Confirmed *Send `json:"confirmed,omitempty"`
}

// Block is time added to a ticket by hand, for work no collector saw.
type Block struct {
	Ticket string    `json:"ticket"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
}

// Send is one confirm: the worklogs it sent and the watermark it sent
// through. Time before Through is sent and never changes, so a later
// change of evidence or settings cannot alter what was sent.
type Send struct {
	At       time.Time `json:"at"`
	Through  time.Time `json:"through"`
	Worklogs []Worklog `json:"worklogs"` // every ticket it covered, those that rounded to nothing too
	// Runs and Stretches are the time it sent as it was derived then, so
	// sent time keeps showing what was sent whatever changes after.
	Runs      []derive.Run     `json:"runs,omitempty"`
	Stretches []derive.Stretch `json:"stretches,omitempty"`
}

// SentDay is the sent time as each send recorded it. ok is false when a
// send has no record, as a day confirmed by an older sift has none.
func (d Decisions) SentDay(start time.Time) (day derive.Day, ok bool) {
	day = derive.Day{Start: start, End: start.AddDate(0, 0, 1)}
	for _, s := range d.Sent {
		if s.Stretches == nil {
			return derive.Day{}, false
		}
		day.Runs = append(day.Runs, s.Runs...)
		day.Stretches = append(day.Stretches, s.Stretches...)
	}
	return day, len(d.Sent) > 0
}

// Through is the latest watermark: everything before it is sent. Zero
// when nothing is.
func (d Decisions) Through() time.Time {
	if len(d.Sent) == 0 {
		return time.Time{}
	}
	return d.Sent[len(d.Sent)-1].Through
}

// SentError refuses a change to time already sent.
type SentError struct{ Through time.Time }

func (e SentError) Error() string {
	return "Sent through " + e.Through.Format("15:04") + ". Change it in Jira."
}

// Sending refuses a change to time before the watermark.
func (d Decisions) Sending(from time.Time) error {
	if t := d.Through(); from.Before(t) {
		return SentError{t.In(from.Location())}
	}
	return nil
}

// State is where a day stands.
type State string

const (
	Open   State = "draft"       // nothing sent
	Partly State = "partly sent" // sent, with more ready to send since
	Sent   State = "sent"        // sent, with nothing more to send
)

// Worklog is one ticket's worklog for the day.
type Worklog struct {
	Ticket   string    `json:"ticket"`
	Minutes  int       `json:"minutes"`
	Counted  int       `json:"counted"`  // from evidence, before decisions
	Assigned int       `json:"assigned"` // meetings, gaps and ticketless time given to it
	ByHand   int       `json:"byHand"`   // what the adjustment changed, after the floor
	Agent    int       `json:"agent"`    // of Counted, minutes from agent activity
	Started  time.Time `json:"started"`
	Raw      int       `json:"raw"` // Counted + Assigned + ByHand: the minutes before rounding
}

// Draft is what a day would send now, what it already sent, and what is
// left unassigned since the last send.
type Draft struct {
	Date       string            `json:"date"`
	Worklogs   []Worklog         `json:"worklogs"` // ready to send: what a confirm would send now
	Total      int               `json:"total"`
	Unassigned derive.Unassigned `json:"unassigned"`
	State      State             `json:"state"`
	Sent       []Worklog         `json:"sent"` // per ticket, every send's minutes added up
	SentTotal  int               `json:"sentTotal"`
	Through    time.Time         `json:"through,omitzero"` // the latest watermark
	Confirmed  bool              `json:"confirmed"`        // State is Sent
	tally      []Worklog         // every ticket with time since the watermark, for the send to record
}

// Tally is what a confirm records for its send: every ticket with time
// since the watermark, with what it sends now (perhaps nothing, after
// rounding) and its raw minutes, so the next send rounds the running total.
func (d Draft) Tally() []Worklog { return d.tally }

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

// Of builds the draft under dec for day, which is derived from the latest
// watermark on (derive's Since), rounding to round minutes (0, 5 or 15).
// With rounding, each ticket's running total for the day is rounded and
// what was already sent is taken off it, so sending in parts never sends
// more than sending once would.
func Of(day derive.Day, dec Decisions, round int) Draft {
	d := Draft{Date: day.Start.Format("2006-01-02"), Unassigned: day.Unassigned, Through: dec.Through(), Sent: []Worklog{}}
	sentRaw, sentMin := map[string]int{}, map[string]int{}
	sent := map[string]*Worklog{}
	for _, s := range dec.Sent {
		for _, w := range s.Worklogs {
			sentRaw[w.Ticket] += w.Raw
			sentMin[w.Ticket] += w.Minutes
			if w.Minutes == 0 {
				continue
			}
			if x, ok := sent[w.Ticket]; ok {
				x.Minutes += w.Minutes
				continue
			}
			sent[w.Ticket] = &Worklog{Ticket: w.Ticket, Minutes: w.Minutes, Started: w.Started}
		}
	}
	for _, w := range sent {
		d.Sent = append(d.Sent, *w)
		d.SentTotal += w.Minutes
	}
	sort.Slice(d.Sent, func(i, j int) bool {
		if !d.Sent[i].Started.Equal(d.Sent[j].Started) {
			return d.Sent[i].Started.Before(d.Sent[j].Started)
		}
		return d.Sent[i].Ticket < d.Sent[j].Ticket
	})
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
		w.Raw = max(base+dec.Adjust[w.Ticket], 0)
		w.ByHand = w.Raw - base
		w.Minutes = w.Raw
		if round != 0 {
			w.Minutes = max(roundTo(sentRaw[w.Ticket]+w.Raw, round)-sentMin[w.Ticket], 0)
		}
		if w.Raw >= 1 || w.Minutes >= 1 {
			d.tally = append(d.tally, *w)
		}
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
	switch {
	case len(dec.Sent) == 0:
		d.State = Open
	case len(d.Sendable()) > 0:
		d.State = Partly
	default:
		d.State, d.Confirmed = Sent, true
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
