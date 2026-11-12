package derive

import (
	"math/rand"
	"reflect"
	"testing"
	"time"
	_ "time/tzdata"
)

var day0 = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)

func hm(h, m int) time.Time { return day0.Add(time.Duration(h*60+m) * time.Minute) }

func ev(t time.Time, k string) Event { return Event{At: t, Ticket: k} }

func totals(d Day) map[string]int {
	out := map[string]int{}
	for _, t := range d.Totals {
		out[t.Ticket] = t.Minutes
	}
	return out
}

func TestRules(t *testing.T) {
	cases := []struct {
		name       string
		events     []Event
		meetings   []Meeting
		settings   Settings
		want       map[string]int
		unassigned Unassigned
		stretches  int
	}{
		{"one event earns the tail", []Event{ev(hm(9, 0), "A")}, nil, Defaults,
			map[string]int{"A": 5}, Unassigned{}, 1},
		{"hopping splits at the midpoint", []Event{ev(hm(9, 0), "A"), ev(hm(9, 6), "B")}, nil, Defaults,
			map[string]int{"A": 3, "B": 8}, Unassigned{}, 1},
		{"the idle gap itself still joins", []Event{ev(hm(9, 0), "A"), ev(hm(9, 10), "A")}, nil, Defaults,
			map[string]int{"A": 15}, Unassigned{}, 1},
		{"a longer silence is a gap", []Event{ev(hm(9, 0), "A"), ev(hm(9, 30), "A")}, nil, Defaults,
			map[string]int{"A": 10}, Unassigned{Gaps: 25}, 3},
		{"a silence shorter than the idle gap is not listed", []Event{ev(hm(9, 0), "A"), ev(hm(9, 12), "A")}, nil, Defaults,
			map[string]int{"A": 10}, Unassigned{}, 2},
		{"a meeting takes every minute it covers",
			[]Event{ev(hm(9, 0), "A"), ev(hm(9, 5), "A"), ev(hm(9, 10), "A")},
			[]Meeting{{hm(9, 4), hm(9, 8), "standup"}}, Defaults,
			map[string]int{"A": 11}, Unassigned{Meetings: 4}, 3},
		{"the setting lets events beat a meeting",
			[]Event{ev(hm(9, 0), "A"), ev(hm(9, 5), "A"), ev(hm(9, 10), "A")},
			[]Meeting{{hm(9, 4), hm(9, 8), "standup"}}, Settings{IdleGap: 10, Tail: 5, EventsWin: true},
			map[string]int{"A": 15}, Unassigned{}, 1},
		{"a meeting with no evidence around it", nil, []Meeting{{hm(14, 0), hm(14, 30), "1:1"}}, Defaults,
			map[string]int{}, Unassigned{Meetings: 30}, 1},
		{"an event with no ticket is unassigned", []Event{ev(hm(9, 0), "")}, nil, Defaults,
			map[string]int{}, Unassigned{Ticketless: 5}, 1},
		{"a wider idle gap joins more", []Event{ev(hm(9, 0), "A"), ev(hm(9, 14), "A")}, nil, Settings{IdleGap: 15, Tail: 5},
			map[string]int{"A": 19}, Unassigned{}, 1},
		{"no events, no meetings", nil, nil, Defaults, map[string]int{}, Unassigned{}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := For(day0, c.events, c.meetings, c.settings)
			if got := totals(d); !reflect.DeepEqual(got, c.want) {
				t.Errorf("totals = %v, want %v", got, c.want)
			}
			if d.Unassigned != c.unassigned {
				t.Errorf("unassigned = %+v, want %+v", d.Unassigned, c.unassigned)
			}
			if len(d.Stretches) != c.stretches {
				t.Errorf("%d stretches, want %d: %+v", len(d.Stretches), c.stretches, d.Stretches)
			}
		})
	}
}

func TestMidnightSplitsAStretch(t *testing.T) {
	events := []Event{ev(hm(23, 58), "A"), ev(hm(24, 3), "A")}
	if got := totals(For(day0, events, nil, Defaults))["A"]; got != 2 {
		t.Errorf("before midnight A = %dm, want 2m", got)
	}
	next := For(day0.AddDate(0, 0, 1), events, nil, Defaults)
	if got := totals(next)["A"]; got != 8 {
		t.Errorf("after midnight A = %dm, want 8m", got)
	}
	if !next.Stretches[0].From.Equal(day0.AddDate(0, 0, 1)) {
		t.Errorf("the next day's stretch starts %v, want midnight", next.Stretches[0].From)
	}
}

func TestFirstCountedMinute(t *testing.T) {
	d := For(day0, []Event{ev(hm(9, 0), "A"), ev(hm(9, 6), "B"), ev(hm(11, 0), "A")}, nil, Defaults)
	if !d.Totals[0].First.Equal(hm(9, 0)) || !d.Totals[1].First.Equal(hm(9, 3)) {
		t.Errorf("first minutes = %v, %v", d.Totals[0].First, d.Totals[1].First)
	}
}

// randomDay is a day of up to 300 events on up to four tickets, some with no
// ticket, and up to three meetings that may overlap.
func randomDay(r *rand.Rand, start time.Time) ([]Event, []Meeting) {
	keys := []string{"", "A", "B", "C", "D"}
	ev := make([]Event, r.Intn(300))
	for i := range ev {
		ev[i] = Event{start.Add(time.Duration(r.Intn(26*60)-60) * time.Minute), keys[r.Intn(len(keys))]}
	}
	ms := make([]Meeting, r.Intn(4))
	for i := range ms {
		from := start.Add(time.Duration(r.Intn(24*60)) * time.Minute)
		ms[i] = Meeting{from, from.Add(time.Duration(15+r.Intn(120)) * time.Minute), "m"}
	}
	return ev, ms
}

// TestInvariants checks, on generated days, what the review promises:
// counted time never exceeds the day, every column of the stretch table
// adds up to its ticket's total, stretches never overlap, and the order
// of the input never changes the result.
func TestInvariants(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	starts := []time.Time{day0, time.Date(2026, 11, 1, 0, 0, 0, 0, ny), time.Date(2026, 3, 8, 0, 0, 0, 0, ny)}
	r := rand.New(rand.NewSource(1))
	for n := 0; n < 3000; n++ {
		start := starts[n%len(starts)]
		events, meetings := randomDay(r, start)
		s := Settings{IdleGap: 1 + r.Intn(30), Tail: r.Intn(10), EventsWin: r.Intn(2) == 0}
		d := For(start, events, meetings, s)

		dayLen := int(d.End.Sub(d.Start) / time.Minute)
		sum := d.Unassigned.Meetings + d.Unassigned.Gaps + d.Unassigned.Ticketless
		for _, tt := range d.Totals {
			sum += tt.Minutes
		}
		if sum > dayLen {
			t.Fatalf("day %d counts %dm in a %dm day", n, sum, dayLen)
		}

		cols := map[string]int{}
		var un Unassigned
		var prev time.Time
		for i, st := range d.Stretches {
			if i > 0 && st.From.Before(prev) {
				t.Fatalf("day %d: stretch %d starts %v before the last ended %v", n, i, st.From, prev)
			}
			prev = st.To
			inside := st.Unassigned
			for k, m := range st.Tickets {
				cols[k] += m
				inside += m
			}
			if inside != st.Minutes() {
				t.Fatalf("day %d: stretch %d accounts for %dm of its %dm", n, i, inside, st.Minutes())
			}
			switch st.Kind {
			case InMeet:
				un.Meetings += st.Unassigned
			case Silence:
				un.Gaps += st.Unassigned
			default:
				un.Ticketless += st.Unassigned
			}
		}
		runs := map[string]int{}
		for _, r := range d.Runs {
			if r.Kind == Work && r.Ticket != "" {
				runs[r.Ticket] += int(r.To.Sub(r.From) / time.Minute)
			}
		}
		if got := totals(d); !reflect.DeepEqual(runs, got) {
			t.Fatalf("day %d: runs %v, totals %v", n, runs, got)
		}
		if got := totals(d); !reflect.DeepEqual(cols, got) {
			t.Fatalf("day %d: columns %v, totals %v", n, cols, got)
		}
		if un != d.Unassigned {
			t.Fatalf("day %d: stretches leave %+v unassigned, the day says %+v", n, un, d.Unassigned)
		}

		shuffled := append([]Event(nil), events...)
		r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		if again := For(start, shuffled, meetings, s); !reflect.DeepEqual(totals(again), totals(d)) || again.Unassigned != d.Unassigned {
			t.Fatalf("day %d: shuffling the events changed the day", n)
		}
	}
}
