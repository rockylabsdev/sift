// Package calendar reads the user's iCalendar subscription and turns each
// day of it into meetings, plus the events it left out and why.
package calendar

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
)

// Meeting is one occurrence of a calendar event, clipped to the day.
type Meeting struct {
	UID   string    `json:"uid"`
	Title string    `json:"title"`
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
}

// Reason is why an occurrence is not a meeting.
type Reason string

const (
	AllDay     Reason = "all-day"
	Cancelled  Reason = "cancelled"
	Declined   Reason = "declined"
	Free       Reason = "free"
	Alone      Reason = "no other attendee"
	Unreadable Reason = "unreadable"
	ByYou      Reason = "skipped" // the user said they were not in it
)

// Skipped is an occurrence on the day that was left out.
type Skipped struct {
	Meeting
	Reason Reason `json:"reason"`
	// Detail says more when Reason is Unreadable.
	Detail string `json:"detail,omitempty"`
}

// Unlisted is how events that name no attendee and no organizer count.
// Published Outlook feeds name no one on any event.
type Unlisted string

const (
	Detect     Unlisted = ""        // as meetings when no event in the feed names anyone
	AsMeetings Unlisted = "meeting" // as meetings, subject to the other rules
	AsAlone    Unlisted = "skip"    // left out as Alone
)

// people is who an event names.
type people int

const (
	unlisted people = iota // no attendee and no organizer
	alone                  // only the owner, rooms and resources
	others
)

// event is one VEVENT, read into what the rules need.
type event struct {
	uid, title string
	start      stamp
	length     time.Duration
	skip       Reason // why it is left out, before who it names; empty otherwise
	people     people
	rule       *rrule.RRule
	extra      []stamp        // RDATE
	recurrence *time.Time     // the RECURRENCE-ID of an override
	excluded   map[int64]bool // unix seconds: EXDATEs, and occurrences an override replaces
}

// widest is the furthest any zone is from UTC, bounding the wall times
// that can land on the day.
const widest = 14 * time.Hour

// Feed is a calendar feed, read once, that gives any day's meetings.
type Feed struct {
	events     []*event
	unreadable []Skipped
	listed     bool // some event names an attendee or an organizer
	// At is when the feed was fetched.
	At time.Time
}

// Counts is whether rule counts f's unlisted events as meetings.
func (f *Feed) Counts(rule Unlisted) bool {
	return rule == AsMeetings || rule == Detect && !f.listed
}

// Listed is whether some event in f names an attendee or an organizer.
func (f *Feed) Listed() bool { return f.listed }

// Parse reads an iCalendar feed. Floating times and DATE values are read
// in loc. owner is the user's email address. The error is set only when
// the feed is not iCalendar; an event that cannot be read is Skipped as
// Unreadable on every day.
func Parse(ics []byte, owner string, loc *time.Location) (*Feed, error) {
	comps, err := decode(ics)
	if err != nil {
		return nil, err
	}
	z := newZones(comps, loc)
	f := &Feed{}
	masters := map[string]*event{}
	for _, c := range comps {
		if c.Name != ical.CompEvent {
			continue
		}
		e, err := read(c, strings.ToLower(owner), z)
		if err != nil {
			uid, _ := c.Props.Text(ical.PropUID)
			title, _ := c.Props.Text(ical.PropSummary)
			f.unreadable = append(f.unreadable, Skipped{Meeting{UID: uid, Title: title}, Unreadable, err.Error()})
			continue
		}
		f.events = append(f.events, e)
		f.listed = f.listed || e.people != unlisted
		if e.recurrence == nil {
			masters[e.uid] = e
		}
	}
	for _, e := range f.events {
		if master := masters[e.uid]; e.recurrence != nil && master != nil {
			master.excluded[e.recurrence.Unix()] = true
		}
	}
	return f, nil
}

// Day returns the occurrences that overlap the local calendar day
// containing day, in day's Location, clipped to it. rule says how events
// that name no one count.
func (f *Feed) Day(day time.Time, rule Unlisted) ([]Meeting, []Skipped) {
	counts := f.Counts(rule)
	y, m, d := day.Date()
	loc := day.Location()
	from := time.Date(y, m, d, 0, 0, 0, 0, loc)
	to := time.Date(y, m, d+1, 0, 0, 0, 0, loc)

	var meetings []Meeting
	skipped := append([]Skipped(nil), f.unreadable...)
	for _, e := range f.events {
		reason := e.skip
		if reason == "" && (e.people == alone || e.people == unlisted && !counts) {
			reason = Alone
		}
		for _, s := range e.occurrences(from.UTC().Add(-e.length-widest), to.UTC().Add(widest)) {
			end := s.Add(e.length)
			if !s.Before(to) || !end.After(from) {
				continue
			}
			o := Meeting{UID: e.uid, Title: e.title, From: maxTime(s, from).In(loc), To: minTime(end, to).In(loc)}
			if reason != "" {
				skipped = append(skipped, Skipped{Meeting: o, Reason: reason})
			} else {
				meetings = append(meetings, o)
			}
		}
	}
	sort.SliceStable(meetings, func(i, j int) bool { return less(meetings[i], meetings[j]) })
	sort.SliceStable(skipped, func(i, j int) bool { return less(skipped[i].Meeting, skipped[j].Meeting) })
	return meetings, skipped
}

// occurrences are the event's start instants whose wall times fall in [lo, hi].
func (e *event) occurrences(lo, hi time.Time) []time.Time {
	walls := []time.Time{e.start.wall}
	if e.rule != nil {
		walls = e.rule.Between(lo, hi, true)
	}
	var out []time.Time
	add := func(t time.Time) {
		if !e.excluded[t.Unix()] {
			out = append(out, t)
		}
	}
	for _, w := range walls {
		add(e.start.zone(w))
	}
	for _, s := range e.extra {
		add(s.at())
	}
	return out
}

// decode returns the components of every VCALENDAR in the feed.
func decode(ics []byte) ([]*ical.Component, error) {
	dec := ical.NewDecoder(bytes.NewReader(ics))
	var comps []*ical.Component
	for n := 0; ; n++ {
		cal, err := dec.Decode()
		if errors.Is(err, io.EOF) {
			if n == 0 {
				return nil, errors.New("not an iCalendar feed: no VCALENDAR")
			}
			return comps, nil
		}
		if err != nil {
			return nil, fmt.Errorf("not an iCalendar feed: %w", err)
		}
		comps = append(comps, cal.Children...)
	}
}

func read(c *ical.Component, owner string, z zones) (*event, error) {
	e := &event{excluded: map[int64]bool{}}
	e.uid, _ = c.Props.Text(ical.PropUID)
	e.title, _ = c.Props.Text(ical.PropSummary)

	p := c.Props.Get(ical.PropDateTimeStart)
	if p == nil {
		return nil, errors.New("no start time")
	}
	var err error
	if e.start, err = z.stamp(p); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	switch {
	case c.Props.Get(ical.PropDateTimeEnd) != nil:
		end, err := z.stamp(c.Props.Get(ical.PropDateTimeEnd))
		if err != nil {
			return nil, fmt.Errorf("end: %w", err)
		}
		e.length = end.at().Sub(e.start.at())
	case c.Props.Get(ical.PropDuration) != nil:
		if e.length, err = c.Props.Get(ical.PropDuration).Duration(); err != nil {
			return nil, fmt.Errorf("duration: %w", err)
		}
	case e.start.date:
		e.length = 24 * time.Hour
	}

	if p := c.Props.Get(ical.PropRecurrenceID); p != nil {
		id, err := z.stamp(p)
		if err != nil {
			return nil, fmt.Errorf("recurrence id: %w", err)
		}
		t := id.at()
		e.recurrence = &t
	} else {
		opt, err := c.Props.RecurrenceRule()
		if err != nil {
			return nil, fmt.Errorf("recurrence rule: %w", err)
		}
		if opt != nil {
			opt.Dtstart = e.start.wall
			if strings.HasSuffix(untilValue(c), "Z") {
				// A UTC UNTIL is an instant; recurrence compares wall times.
				opt.Until = opt.Until.Add(opt.Until.Sub(e.start.zone(opt.Until)))
			}
			if e.rule, err = rrule.NewRRule(*opt); err != nil {
				return nil, fmt.Errorf("recurrence rule: %w", err)
			}
		}
		for _, p := range c.Props.Values(ical.PropRecurrenceDates) {
			s, err := z.stamps(&p)
			if err != nil {
				return nil, fmt.Errorf("rdate: %w", err)
			}
			e.extra = append(e.extra, s...)
		}
		for _, p := range c.Props.Values(ical.PropExceptionDates) {
			s, err := z.stamps(&p)
			if err != nil {
				return nil, fmt.Errorf("exdate: %w", err)
			}
			for _, x := range s {
				e.excluded[x.at().Unix()] = true
			}
		}
	}

	status, _ := c.Props.Text(ical.PropStatus)
	cancelled := strings.EqualFold(status, "CANCELLED")
	transp, _ := c.Props.Text(ical.PropTransparency)
	free := strings.EqualFold(transp, "TRANSPARENT")

	// The organizer counts as another person: a 1:1 invitation may list
	// only the invitee.
	declined := false
	organizer := c.Props.Get(ical.PropOrganizer)
	attendees := c.Props.Values(ical.PropAttendee)
	if organizer != nil || len(attendees) > 0 {
		e.people = alone
	}
	if organizer != nil && address(organizer) != owner {
		e.people = others
	}
	for _, p := range attendees {
		if address(&p) == owner {
			// No owner ATTENDEE at all, as when the owner organizes, reads as accepted.
			declined = strings.EqualFold(p.Params.Get(ical.ParamParticipationStatus), "DECLINED")
			continue
		}
		switch strings.ToUpper(p.Params.Get(ical.ParamCalendarUserType)) {
		case "ROOM", "RESOURCE":
		default:
			e.people = others
		}
	}
	// One reason per occurrence; the first that applies.
	switch {
	case e.start.date:
		e.skip = AllDay
	case cancelled:
		e.skip = Cancelled
	case declined:
		e.skip = Declined
	case free:
		e.skip = Free
	}
	return e, nil
}

func untilValue(c *ical.Component) string {
	for _, part := range strings.Split(c.Props.Get(ical.PropRecurrenceRule).Value, ";") {
		if k, v, _ := strings.Cut(part, "="); strings.EqualFold(k, "UNTIL") {
			return v
		}
	}
	return ""
}

// address is the lowercased email a calendar user property names.
func address(p *ical.Prop) string {
	if e := p.Params.Get(ical.ParamEmail); e != "" {
		return strings.ToLower(e)
	}
	v := strings.ToLower(strings.TrimSpace(p.Value))
	return strings.TrimPrefix(v, "mailto:")
}

func less(a, b Meeting) bool {
	if !a.From.Equal(b.From) {
		return a.From.Before(b.From)
	}
	return a.Title < b.Title
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
