package cli

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rockylabsdev/sift/internal/calendar"
	"github.com/rockylabsdev/sift/internal/derive"
	"github.com/rockylabsdev/sift/internal/draft"
	"github.com/rockylabsdev/sift/internal/evidence"
)

// day reads the evidence around a local calendar day: the day itself plus
// enough either side for a stretch that crosses midnight, with retags
// applied, and returns the retags it applied. Every reader of a day goes
// through here. edit, if not nil, changes the retags read before they
// apply, to read the day as it would be after a retag not yet saved.
func (r *run) day(date string, edit func([]evidence.Retag) []evidence.Retag) (time.Time, []evidence.Event, []evidence.Retag, error) {
	now := r.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if date != "" {
		d, err := time.ParseInLocation("2006-01-02", date, now.Location())
		if err != nil {
			return start, nil, nil, usageError{fmt.Sprintf("--date takes YYYY-MM-DD, not %q", date)}
		}
		start = d
	}
	dir, err := r.dataDir()
	if err != nil {
		return start, nil, nil, err
	}
	end := time.Date(start.Year(), start.Month(), start.Day()+1, 0, 0, 0, 0, start.Location())
	events, problems, err := evidence.Log{Dir: dir}.Between(start.Add(-2*time.Hour), end.Add(2*time.Hour))
	if err != nil {
		return start, nil, nil, err
	}
	rs, more, err := evidence.Retags{Dir: dir}.All()
	if err != nil {
		return start, nil, nil, err
	}
	for _, p := range append(problems, more...) {
		r.notes = append(r.notes, fmt.Sprintf("skipped %s line %d: %s", p.File, p.Line, p.Err))
	}
	if edit != nil {
		rs = edit(rs)
	}
	// Sent time keeps showing what was sent: the events a send covered take
	// only the retags made before it. (A broken decisions file is load's to
	// report.)
	dec, _ := (draft.Store{Dir: dir}).Load(start.Format("2006-01-02"))
	var out []evidence.Event
	from := time.Time{}
	for _, s := range dec.Sent {
		var before []evidence.Retag
		for _, rt := range rs {
			if !rt.At.After(s.At) {
				before = append(before, rt)
			}
		}
		out = append(out, evidence.Apply(between(events, from, s.Through), before)...)
		from = s.Through
	}
	out = append(out, evidence.Apply(between(events, from, time.Time{}), rs)...)
	return start, out, rs, nil
}

// between is the events at or after from and, unless to is zero, before to.
func between(events []evidence.Event, from, to time.Time) []evidence.Event {
	var out []evidence.Event
	for _, e := range events {
		if !e.At.Before(from) && (to.IsZero() || e.At.Before(to)) {
			out = append(out, e)
		}
	}
	return out
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
	start, all, _, err := r.day(date, nil)
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
	Skipped   []derive.Meeting // meetings the user skipped, left out of Day
	Before    derive.Day       // the day up to the latest watermark: what was sent, as derived now
	Retags    []evidence.Retag // the retags applied, in the order made
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
func (r *run) load(date string) (Loaded, error) { return r.loadAs(date, nil, true) }

// loadAs is load with day's edit to the retags. Unless wait, a calendar
// not yet downloaded is left Pending.
func (r *run) loadAs(date string, edit func([]evidence.Retag) []evidence.Retag, wait bool) (Loaded, error) {
	var l Loaded
	cfg, err := r.config()
	if err != nil {
		return l, err
	}
	start, events, rs, err := r.day(date, edit)
	if err != nil {
		return l, err
	}
	dir, _ := r.dataDir()
	l = Loaded{Start: start, Config: cfg, Store: draft.Store{Dir: dir}, Retags: rs}
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
	set := derive.Settings{IdleGap: cfg.IdleGap, Tail: derive.Defaults.Tail, EventsWin: cfg.EventsWin, Agent: derive.AgentMode(cfg.AgentTime), Until: r.Now(), Since: l.Decisions.Through()}
	for _, b := range l.Decisions.Blocks {
		set.Blocks = append(set.Blocks, derive.Block{From: b.From, To: b.To, Ticket: b.Ticket})
	}
	var all, meetings []derive.Meeting
	if r.withCalendar {
		l.Calendar = r.meetings(cfg, start, wait)
		for _, m := range l.Calendar.Meetings {
			all = append(all, derive.Meeting{ID: meetingID(m), From: m.From, To: m.To, Title: m.Title})
		}
	}
	if r.demoMeetings != nil {
		all = r.demoMeetings(start)
	}
	for _, m := range all {
		if slices.Contains(l.Decisions.Skip, m.ID) {
			l.Skipped = append(l.Skipped, m)
		} else {
			meetings = append(meetings, m)
		}
	}
	l.Day = derive.For(start, in, meetings, set)
	if t := set.Since; !t.IsZero() {
		set.Since, set.Until = time.Time{}, minTime(set.Until, t.Add(-time.Nanosecond))
		l.Before = derive.For(start, in, meetings, set)
	}
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
	dr := Draft{Draft: draft.Of(l.Day, l.Decisions, l.Config.Round), Stretches: nonNil(l.Day.Stretches), Meetings: calendarNote(l.calendar()), Skipped: nonNil(l.calendar().Skipped), Settings: l.Config}
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
		for _, wl := range d.Sent {
			fmt.Fprintf(w, "| %s (sent) | %s | %s |\n", wl.Ticket, dur(wl.Minutes), clock(wl.Started))
		}
		if len(d.Sent) > 0 {
			fmt.Fprintf(w, "| **Sent through %s** | **%s** | |\n", clock(d.Through), dur(d.SentTotal))
		}
		for _, wl := range d.Worklogs {
			fmt.Fprintf(w, "| %s | %s | %s |\n", wl.Ticket, dur(wl.Minutes), clock(wl.Started))
		}
		fmt.Fprintf(w, "| **Ready to send** | **%s** | |\n\nUnassigned %s: meetings %s, gaps %s, no ticket %s.\n",
			dur(d.Total), dur(un), dur(d.Unassigned.Meetings), dur(d.Unassigned.Gaps), dur(d.Unassigned.Ticketless))
		return
	}
	fmt.Fprintf(w, "%s · %s\n\n", day.Format("Mon 2 Jan"), d.State)
	if len(d.Sent) > 0 {
		fmt.Fprintf(w, "  sent through %s %8s\n", clock(d.Through), dur(d.SentTotal))
		for _, wl := range d.Sent {
			fmt.Fprintf(w, "    %-10s %8s\n", wl.Ticket, dur(wl.Minutes))
		}
		fmt.Fprintln(w)
	}
	if len(d.Worklogs) == 0 && len(d.Sent) == 0 {
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

// meetingID names one occurrence of a calendar event within its day.
func meetingID(m calendar.Meeting) string {
	id := m.UID
	if id == "" {
		id = m.Title
	}
	return id + "@" + m.From.Format("15:04")
}

// calendar is the day's calendar after the user's decisions: the
// meetings they skipped move to the events left out.
func (l Loaded) calendar() calendar.Day {
	c := l.Calendar
	c.Meetings, c.Skipped = nil, append([]calendar.Skipped{}, l.Calendar.Skipped...)
	for _, m := range l.Calendar.Meetings {
		if slices.Contains(l.Decisions.Skip, meetingID(m)) {
			c.Skipped = append(c.Skipped, calendar.Skipped{Meeting: m, Reason: calendar.ByYou})
		} else {
			c.Meetings = append(c.Meetings, m)
		}
	}
	sort.SliceStable(c.Skipped, func(i, j int) bool { return c.Skipped[i].From.Before(c.Skipped[j].From) })
	return c
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
