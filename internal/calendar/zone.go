package calendar

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // TZIDs must resolve even where the system has no zoneinfo.

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
)

// A wall time is a clock reading held in a UTC time.Time. Recurrence runs
// on wall times, so a 09:00 meeting stays at 09:00 across a DST change.
// A zone turns a wall time into the instant it names.
type zone func(wall time.Time) time.Time

func inLocation(loc *time.Location) zone {
	return func(w time.Time) time.Time {
		return time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), 0, loc)
	}
}

func utc(w time.Time) time.Time { return w }

// stamp is a DATE or DATE-TIME property value.
type stamp struct {
	wall time.Time
	zone zone
	date bool // a DATE, with no time of day
}

func (s stamp) at() time.Time { return s.zone(s.wall) }

// zones resolves TZIDs. IANA names load from tzdata. Other names, such as
// the Windows names Outlook feeds use, are read from the feed's VTIMEZONE.
type zones struct {
	floating zone
	feed     map[string]zone
}

func newZones(comps []*ical.Component, loc *time.Location) zones {
	z := zones{floating: inLocation(loc), feed: map[string]zone{}}
	for _, c := range comps {
		if c.Name != ical.CompTimezone {
			continue
		}
		id, _ := c.Props.Text(ical.PropTimezoneID)
		if vz, ok := vtimezone(c); ok {
			z.feed[id] = vz
		}
	}
	return z
}

func (z zones) lookup(tzid string) (zone, error) {
	if loc, err := time.LoadLocation(strings.TrimPrefix(tzid, "/")); err == nil {
		return inLocation(loc), nil
	}
	if vz, ok := z.feed[tzid]; ok {
		return vz, nil
	}
	return nil, fmt.Errorf("unknown time zone %q", tzid)
}

// stamps reads every value of a property; EXDATE and RDATE may list several.
func (z zones) stamps(p *ical.Prop) ([]stamp, error) {
	zn := z.floating
	if tzid := p.Params.Get(ical.ParamTimezoneID); tzid != "" {
		var err error
		if zn, err = z.lookup(tzid); err != nil {
			return nil, err
		}
	}
	var out []stamp
	for _, v := range strings.Split(strings.TrimSpace(p.Value), ",") {
		s := stamp{zone: zn}
		var err error
		switch len(v) {
		case len("20060102"):
			s.wall, err = time.Parse("20060102", v)
			s.date, s.zone = true, z.floating
		case len("20060102T150405Z"):
			s.wall, err = time.Parse("20060102T150405Z", v)
			s.zone = utc
		default:
			s.wall, err = time.Parse("20060102T150405", v)
		}
		if err != nil {
			return nil, fmt.Errorf("bad time %q", v)
		}
		out = append(out, s)
	}
	return out, nil
}

func (z zones) stamp(p *ical.Prop) (stamp, error) {
	s, err := z.stamps(p)
	if err != nil {
		return stamp{}, err
	}
	return s[0], nil
}

// observance is one STANDARD or DAYLIGHT part of a VTIMEZONE.
type observance struct {
	onset    time.Time // wall time in the offset before it
	rule     *rrule.ROption
	from, to int // offsets east of UTC, in seconds
	name     string
}

// latest is the last onset at or before wall, or false if none is.
func (o observance) latest(wall time.Time) (time.Time, bool) {
	if wall.Before(o.onset) {
		return time.Time{}, false
	}
	if o.rule == nil {
		return o.onset, true
	}
	opt := *o.rule
	opt.Dtstart = o.onset
	// rrule-go stops short when iterating from far back, and Outlook
	// starts its zones in 1601. Yearly rules restart a year before.
	if y := wall.Year() - 1; opt.Freq == rrule.YEARLY && opt.Interval <= 1 && opt.Count == 0 && y > o.onset.Year() {
		opt.Dtstart = o.onset.AddDate(y-o.onset.Year(), 0, 0)
	}
	r, err := rrule.NewRRule(opt)
	if err != nil {
		return time.Time{}, false
	}
	t := r.Before(wall, true)
	return t, !t.IsZero()
}

func vtimezone(c *ical.Component) (zone, bool) {
	var obs []observance
	for _, part := range c.Children {
		if part.Name != ical.CompTimezoneStandard && part.Name != ical.CompTimezoneDaylight {
			continue
		}
		start := part.Props.Get(ical.PropDateTimeStart)
		from := part.Props.Get(ical.PropTimezoneOffsetFrom)
		to := part.Props.Get(ical.PropTimezoneOffsetTo)
		if start == nil || from == nil || to == nil {
			return nil, false
		}
		var o observance
		var err error
		if o.onset, err = time.Parse("20060102T150405", start.Value); err != nil {
			return nil, false
		}
		if o.from, err = offset(from.Value); err != nil {
			return nil, false
		}
		if o.to, err = offset(to.Value); err != nil {
			return nil, false
		}
		o.name, _ = part.Props.Text(ical.PropTimezoneName)
		if opt, err := part.Props.RecurrenceRule(); err != nil {
			return nil, false
		} else if opt != nil {
			if _, err := rrule.NewRRule(*opt); err != nil {
				return nil, false
			}
			o.rule = opt
		}
		obs = append(obs, o)
	}
	if len(obs) == 0 {
		return nil, false
	}
	return func(w time.Time) time.Time {
		var best *observance
		var bestAt time.Time
		for i := range obs {
			if t, ok := obs[i].latest(w); ok && (best == nil || t.After(bestAt)) {
				best, bestAt = &obs[i], t
			}
		}
		off, name := obs[0].from, obs[0].name
		if best != nil {
			off, name = best.to, best.name
		}
		return w.Add(-time.Duration(off) * time.Second).In(time.FixedZone(name, off))
	}, true
}

// offset parses a UTC offset such as -0500 or +053000.
func offset(v string) (int, error) {
	if len(v) != 5 && len(v) != 7 || (v[0] != '+' && v[0] != '-') {
		return 0, fmt.Errorf("bad offset %q", v)
	}
	n := 0
	for i, unit := range []int{3600, 60, 1} {
		if 1+2*i >= len(v) {
			break
		}
		d, err := strconv.Atoi(v[1+2*i : 3+2*i])
		if err != nil {
			return 0, fmt.Errorf("bad offset %q", v)
		}
		n += d * unit
	}
	if v[0] == '-' {
		n = -n
	}
	return n, nil
}
