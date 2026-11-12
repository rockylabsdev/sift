package draft

import (
	"errors"
	"testing"
	"time"

	"github.com/rockylabsdev/sift/internal/derive"
)

var day0 = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)

func hm(h, m int) time.Time { return day0.Add(time.Duration(h*60+m) * time.Minute) }

// sample is a day with work on A and B, ticketless time, a meeting and a gap.
func sample() derive.Day {
	ev := []derive.Event{
		{At: hm(9, 0), Ticket: "A"}, {At: hm(9, 6), Ticket: "B"},
		{At: hm(9, 40), Ticket: ""},
		{At: hm(10, 30), Ticket: "A"},
	}
	return derive.For(day0, ev, []derive.Meeting{{From: hm(10, 0), To: hm(10, 20), Title: "standup"}}, derive.Defaults)
}

func find(d Draft, t string) Worklog {
	for _, w := range d.Worklogs {
		if w.Ticket == t {
			return w
		}
	}
	return Worklog{}
}

func stretch(day derive.Day, k derive.Kind) derive.Stretch {
	for _, s := range day.Stretches {
		if s.Kind == k {
			return s
		}
	}
	panic("no " + k)
}

func TestNoDecisionsIsTheDerivedDay(t *testing.T) {
	day := sample()
	d := Of(day, Decisions{}, 0)
	if find(d, "A").Minutes != 8 || find(d, "B").Minutes != 8 || d.Total != 16 {
		t.Fatalf("%+v", d.Worklogs)
	}
	if d.Unassigned != day.Unassigned {
		t.Fatalf("unassigned %+v, want %+v", d.Unassigned, day.Unassigned)
	}
}

func TestAssigningMovesTimeOutOfUnassigned(t *testing.T) {
	day := sample()
	meet, work, gaps := stretch(day, derive.InMeet), derive.Stretch{}, 0
	dec := Decisions{Assign: map[string]string{ID(meet): "B"}}
	for _, s := range day.Stretches {
		switch {
		case s.Kind == derive.Work && s.Unassigned > 0:
			work = s
			dec.Assign[ID(s)] = "C"
		case s.Kind == derive.Silence:
			gaps += s.Minutes()
			dec.Assign[ID(s)] = "A"
		}
	}
	d := Of(day, dec, 0)
	if got := find(d, "B"); got.Assigned != 20 || got.Minutes != 28 {
		t.Errorf("B %+v; want the 20m meeting on top of 8m", got)
	}
	if got := find(d, "A").Assigned; got != gaps || gaps != 54 {
		t.Errorf("A assigned %dm, want all %dm of gaps (29 + 15 + 10)", got, gaps)
	}
	if got := find(d, "C"); got.Minutes != 5 || !got.Started.Equal(work.From) {
		t.Errorf("C %+v; want the 5m ticketless stretch, starting where it does", got)
	}
	if d.Unassigned != (derive.Unassigned{}) {
		t.Errorf("unassigned %+v, want none left", d.Unassigned)
	}
	if d.Total != 16+20+gaps+5 {
		t.Errorf("total %d", d.Total)
	}
}

func TestAdjustingStopsAtZero(t *testing.T) {
	d := Of(sample(), Decisions{Adjust: map[string]int{"A": -60, "B": 7}}, 0)
	if a := find(d, "A"); a.Minutes != 0 || a.ByHand != -8 {
		t.Errorf("A %+v; want 0m and a by-hand of -8m, the change actually made", a)
	}
	if a := find(d, "A"); !a.Started.Equal(hm(9, 0)) {
		t.Errorf("an adjustment moved A's start to %v", a.Started)
	}
	if b := find(d, "B"); b.Minutes != 15 || b.ByHand != 7 {
		t.Errorf("B %+v", b)
	}
	if len(d.Sendable()) != 1 {
		t.Errorf("a 0m worklog is shown but never sent: %+v", d.Sendable())
	}
}

func TestRounding(t *testing.T) {
	d := Of(sample(), Decisions{Adjust: map[string]int{"B": 0}}, 15)
	if find(d, "A").Minutes != 15 || find(d, "A").Counted != 8 {
		t.Fatalf("%+v", d.Worklogs)
	}
}

func TestAConfirmedDayIsWhatWasConfirmed(t *testing.T) {
	frozen := []Worklog{{Ticket: "A", Minutes: 90}}
	d := Of(sample(), Decisions{Confirmed: &Confirmed{Worklogs: frozen}}, 0)
	if !d.Confirmed || d.Total != 90 || len(d.Worklogs) != 1 {
		t.Fatalf("%+v", d)
	}
}

func TestStoreRefusesChangesOnceConfirmed(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, err := s.Update("2026-11-10", func(d *Decisions) error {
		d.Adjust = map[string]int{"A": 5}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Load("2026-11-10")
	if got.Adjust["A"] != 5 {
		t.Fatalf("%+v", got)
	}
	s.Update("2026-11-10", func(d *Decisions) error { d.Confirmed = &Confirmed{}; return nil })
	_, err := s.Update("2026-11-10", func(d *Decisions) error { d.Adjust["A"] = 10; return nil })
	if !errors.Is(err, ErrConfirmed) {
		t.Fatalf("err %v", err)
	}
	days, _ := s.Days()
	if !days["2026-11-10"] {
		t.Fatalf("days %v", days)
	}
}
