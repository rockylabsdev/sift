package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rockylabsdev/sift/internal/calendar"
	"github.com/rockylabsdev/sift/internal/derive"
	"github.com/rockylabsdev/sift/internal/draft"
	"github.com/rockylabsdev/sift/internal/evidence"
)

// day reads the evidence around a local calendar day: the day itself plus
// enough either side for a stretch that crosses midnight.
func (r *run) day(date string) (time.Time, []evidence.Event, error) {
	now := r.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if date != "" {
		d, err := time.ParseInLocation("2006-01-02", date, now.Location())
		if err != nil {
			return start, nil, usageError{fmt.Sprintf("--date takes YYYY-MM-DD, not %q", date)}
		}
		start = d
	}
	dir, err := r.dataDir()
	if err != nil {
		return start, nil, err
	}
	end := time.Date(start.Year(), start.Month(), start.Day()+1, 0, 0, 0, 0, start.Location())
	events, problems, err := evidence.Log{Dir: dir}.Between(start.Add(-2*time.Hour), end.Add(2*time.Hour))
	for _, p := range problems {
		r.notes = append(r.notes, fmt.Sprintf("skipped %s line %d: %s", p.File, p.Line, p.Err))
	}
	return start, events, err
}

func inDay(start time.Time, at time.Time) bool {
	end := time.Date(start.Year(), start.Month(), start.Day()+1, 0, 0, 0, 0, start.Location())
	return !at.Before(start) && at.Before(end)
}

func (r *run) events(args []string) (any, func(io.Writer), error) {
	var date string
	if err := r.flags(args, func(fs *flag.FlagSet) { fs.StringVar(&date, "date", "", "") }); err != nil {
		return nil, nil, err
	}
	start, all, err := r.day(date)
	if err != nil {
		return nil, nil, err
	}
	var events []evidence.Event
	for _, e := range all {
		if inDay(start, e.At) {
			events = append(events, e)
		}
	}
	data := map[string]any{"date": start.Format("2006-01-02"), "events": nonNil(events)}
	return data, func(w io.Writer) {
		if len(events) == 0 {
			fmt.Fprintf(w, "No events on %s.\n", start.Format("Mon 2 Jan"))
			return
		}
		var prev time.Time
		for i, e := range events {
			t, via := "no ticket", ""
			if e.Ticket != nil {
				t, via = *e.Ticket, e.Found.Via+" "+e.Found.Value
			}
			if e.Origin == evidence.Agent {
				via = "agent " + e.Kind
				if e.AgentID != "" {
					via += " " + e.AgentID
				}
				if e.SpanMs > 0 {
					via += fmt.Sprintf(" (%s)", time.Duration(e.SpanMs)*time.Millisecond)
				}
			}
			since := ""
			if i > 0 {
				since = fmt.Sprintf("+%dm", int(e.At.Sub(prev)/time.Minute))
			}
			prev = e.At
			fmt.Fprintf(w, "%s %6s  %-10s %-7s %s\n", e.At.In(start.Location()).Format("15:04"), since, t, e.Kind, via)
		}
	}, nil
}

func nonNil[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}

// Loaded is one day, read and derived, with the user's decisions.
type Loaded struct {
	Start     time.Time
	Events    []evidence.Event // the day's own events
	Day       derive.Day
	Decisions draft.Decisions
	Config    Config
	Store     draft.Store
	Calendar  calendar.Day
}

// Draft is the CLI's view of a day's draft.
type Draft struct {
	draft.Draft
	Stretches []derive.Stretch   `json:"stretches"`
	Meetings  string             `json:"meetings"` // where meetings came from
	Skipped   []calendar.Skipped `json:"skipped"`  // calendar events left out, and why
	Settings  Config             `json:"settings"`
}

// load reads, derives and decides the day date ("" for today).
func (r *run) load(date string) (Loaded, error) {
	var l Loaded
	cfg, err := r.config()
	if err != nil {
		return l, err
	}
	start, events, err := r.day(date)
	if err != nil {
		return l, err
	}
	dir, _ := r.dataDir()
	l = Loaded{Start: start, Config: cfg, Store: draft.Store{Dir: dir}}
	var in []derive.Event
	for _, e := range events {
		k := ""
		if e.Ticket != nil {
			k = *e.Ticket
		}
		in = append(in, derive.Event{At: e.At, Ticket: k, Agent: e.Origin == evidence.Agent, Span: time.Duration(e.SpanMs) * time.Millisecond})
		if inDay(start, e.At) {
			l.Events = append(l.Events, e)
		}
	}
	l.Decisions, err = l.Store.Load(start.Format("2006-01-02"))
	if err != nil {
		return l, err
	}
	set := derive.Settings{IdleGap: cfg.IdleGap, Tail: derive.Defaults.Tail, EventsWin: cfg.EventsWin, Agent: derive.AgentMode(cfg.AgentTime)}
	for _, b := range l.Decisions.Blocks {
		set.Blocks = append(set.Blocks, derive.Block{From: b.From, To: b.To, Ticket: b.Ticket})
	}
	var meetings []derive.Meeting
	if l.Decisions.Confirmed == nil && r.withCalendar {
		l.Calendar = r.meetings(cfg, start)
		for _, m := range l.Calendar.Meetings {
			meetings = append(meetings, derive.Meeting{From: m.From, To: m.To, Title: m.Title})
		}
	}
	if r.demoMeetings != nil {
		meetings = r.demoMeetings(start)
	}
	l.Day = derive.For(start, in, meetings, set)
	return l, nil
}

func (r *run) draft(args []string) (any, func(io.Writer), error) {
	var date string
	var md bool
	if err := r.flags(args, func(fs *flag.FlagSet) {
		fs.StringVar(&date, "date", "", "")
		fs.BoolVar(&md, "md", false, "")
	}); err != nil {
		return nil, nil, err
	}
	if md && r.jsonFlag {
		return nil, nil, usageError{"--md and --json are two formats; pick one"}
	}
	l, err := r.load(date)
	if err != nil {
		return nil, nil, err
	}
	dr := Draft{Draft: draft.Of(l.Day, l.Decisions, l.Config.Round), Stretches: nonNil(l.Day.Stretches), Meetings: calendarNote(l.Calendar), Skipped: nonNil(l.Calendar.Skipped), Settings: l.Config}
	if md {
		r.json = false
	}
	return dr, func(w io.Writer) { writeDraft(w, dr, l.Start.Location(), md) }, nil
}

func dur(n int) string {
	if n < 60 {
		return fmt.Sprintf("%dm", n)
	}
	return fmt.Sprintf("%dh %02dm", n/60, n%60)
}

func writeDraft(w io.Writer, d Draft, loc *time.Location, md bool) {
	day, _ := time.ParseInLocation("2006-01-02", d.Date, loc)
	clock := func(t time.Time) string { return t.In(loc).Format("15:04") }
	un := d.Unassigned.Meetings + d.Unassigned.Gaps + d.Unassigned.Ticketless
	if md {
		fmt.Fprintf(w, "# Draft · %s\n\n| Ticket | Time | From |\n| --- | ---: | ---: |\n", day.Format("Mon 2 Jan 2006"))
		for _, wl := range d.Worklogs {
			fmt.Fprintf(w, "| %s | %s | %s |\n", wl.Ticket, dur(wl.Minutes), clock(wl.Started))
		}
		fmt.Fprintf(w, "| **Ready to send** | **%s** | |\n\nUnassigned %s: meetings %s, gaps %s, no ticket %s.\n",
			dur(d.Total), dur(un), dur(d.Unassigned.Meetings), dur(d.Unassigned.Gaps), dur(d.Unassigned.Ticketless))
		return
	}
	fmt.Fprintf(w, "%s · draft\n\n", day.Format("Mon 2 Jan"))
	if len(d.Worklogs) == 0 {
		fmt.Fprintln(w, "  No counted time.")
	}
	for _, wl := range d.Worklogs {
		var notes []string
		if wl.Agent > 0 {
			notes = append(notes, "agents "+dur(wl.Agent))
		}
		if wl.Assigned > 0 {
			notes = append(notes, "assigned "+dur(wl.Assigned))
		}
		if wl.ByHand != 0 {
			notes = append(notes, fmt.Sprintf("%+dm by hand", wl.ByHand))
		}
		if d.Settings.Round > 0 && wl.Minutes != wl.Counted+wl.Assigned+wl.ByHand {
			notes = append(notes, fmt.Sprintf("rounded to %dm", d.Settings.Round))
		}
		note := ""
		if len(notes) > 0 {
			note = "  (" + strings.Join(notes, ", ") + ")"
		}
		fmt.Fprintf(w, "  %-10s %8s   from %s%s\n", wl.Ticket, dur(wl.Minutes), clock(wl.Started), note)
	}
	fmt.Fprintf(w, "  %-10s %8s   %s\n\n", "ready", dur(d.Total), plural(len(d.Worklogs), "worklog"))
	fmt.Fprintf(w, "  unassigned %8s   meetings %s · gaps %s · no ticket %s\n  %s\n", dur(un), dur(d.Unassigned.Meetings), dur(d.Unassigned.Gaps), dur(d.Unassigned.Ticketless), d.Meetings)
	for _, sk := range d.Skipped {
		fmt.Fprintf(w, "  left out    %s–%s  %s (%s)\n", clock(sk.From), clock(sk.To), sk.Title, sk.Reason)
	}
	if len(d.Stretches) == 0 {
		return
	}
	fmt.Fprintln(w, "\n  stretches")
	for _, s := range d.Stretches {
		var parts []string
		switch s.Kind {
		case derive.Work:
			for _, wl := range d.Worklogs {
				if m := s.Tickets[wl.Ticket]; m > 0 {
					parts = append(parts, wl.Ticket+" "+dur(m))
				}
			}
			for k, m := range s.Tickets {
				if !hasTicket(d.Worklogs, k) {
					parts = append(parts, k+" "+dur(m))
				}
			}
			if s.Unassigned > 0 {
				parts = append(parts, "no ticket "+dur(s.Unassigned))
			}
			parts = append(parts, plural(s.Events, "event"))
		case derive.Silence:
			parts = append(parts, "gap "+dur(s.Minutes()))
		default:
			parts = append(parts, s.Meeting+" "+dur(s.Minutes()))
		}
		fmt.Fprintf(w, "  %s–%s  %s\n", clock(s.From), clock(s.To), strings.Join(parts, " · "))
	}
}

func hasTicket(ws []draft.Worklog, k string) bool {
	for _, w := range ws {
		if w.Ticket == k {
			return true
		}
	}
	return false
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
