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
			[]Meeting{{From: hm(9, 4), To: hm(9, 8), Title: "standup"}}, Defaults,
			map[string]int{"A": 11}, Unassigned{Meetings: 4}, 3},
		{"the setting lets events beat a meeting",
			[]Event{ev(hm(9, 0), "A"), ev(hm(9, 5), "A"), ev(hm(9, 10), "A")},
			[]Meeting{{From: hm(9, 4), To: hm(9, 8), Title: "standup"}}, Settings{IdleGap: 10, Tail: 5, EventsWin: true},
			map[string]int{"A": 15}, Unassigned{}, 1},
		{"a meeting with no evidence around it", nil, []Meeting{{From: hm(14, 0), To: hm(14, 30), Title: "1:1"}}, Defaults,
			map[string]int{}, Unassigned{Meetings: 30}, 1},
		{"an event with no ticket is unassigned", []Event{ev(hm(9, 0), "")}, nil, Defaults,
			map[string]int{}, Unassigned{Ticketless: 5}, 1},
		{"a wider idle gap joins more", []Event{ev(hm(9, 0), "A"), ev(hm(9, 14), "A")}, nil, Settings{IdleGap: 15, Tail: 5},
			map[string]int{"A": 19}, Unassigned{}, 1},
		{"no events, no meetings", nil, nil, Defaults, map[string]int{}, Unassigned{}, 0},
		{"a block owns its minutes ahead of a meeting and evidence",
			[]Event{ev(hm(9, 0), "A"), ev(hm(9, 5), "A")},
			[]Meeting{{From: hm(9, 30), To: hm(10, 0), Title: "planning"}},
			Settings{IdleGap: 10, Tail: 5, Blocks: []Block{{hm(9, 5), hm(9, 45), "B"}}},
			map[string]int{"A": 5, "B": 40}, Unassigned{Meetings: 15}, 2},
		{"a block alone", nil, nil, Settings{IdleGap: 10, Tail: 5, Blocks: []Block{{hm(14, 0), hm(15, 30), "C"}}},
			map[string]int{"C": 90}, Unassigned{}, 1},
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
		ev[i] = Event{At: start.Add(time.Duration(r.Intn(26*60)-60) * time.Minute), Ticket: keys[r.Intn(len(keys))]}
	}
	for i := r.Intn(200); i > 0; i-- { // agent activity, some of it long tool runs
		ev = append(ev, Event{At: start.Add(time.Duration(r.Intn(26*60)-60) * time.Minute), Ticket: keys[r.Intn(len(keys))],
			Agent: true, Span: time.Duration(r.Intn(3)*r.Intn(40)) * time.Minute})
	}
	ms := make([]Meeting, r.Intn(4))
	for i := range ms {
		from := start.Add(time.Duration(r.Intn(24*60)) * time.Minute)
		ms[i] = Meeting{From: from, To: from.Add(time.Duration(15+r.Intn(120)) * time.Minute), Title: "m"}
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
		s := Settings{IdleGap: 1 + r.Intn(30), Tail: r.Intn(10), EventsWin: r.Intn(2) == 0,
			Agent: []AgentMode{AgentFill, AgentWorkday, AgentOff}[r.Intn(3)]}
		for b := r.Intn(3); b > 0; b-- {
			from := start.Add(time.Duration(r.Intn(24*60)) * time.Minute)
			s.Blocks = append(s.Blocks, Block{from, from.Add(time.Duration(r.Intn(180)) * time.Minute), "B"})
		}
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

func ag(t time.Time, k string, span time.Duration) Event {
	return Event{At: t, Ticket: k, Agent: true, Span: span}
}

func agentTotals(d Day) map[string]int {
	out := map[string]int{}
	for _, t := range d.Totals {
		if t.Agent > 0 {
			out[t.Ticket] = t.Agent
		}
	}
	return out
}

func TestAgentTime(t *testing.T) {
	cases := []struct {
		name   string
		events []Event
		mode   AgentMode
		want   map[string]int // minutes per ticket
		agent  map[string]int // of which from agents
		gaps   int
	}{
		{"agents fill the silence after your prompt",
			[]Event{ev(hm(9, 0), "A"), ag(hm(9, 30), "A", 0), ag(hm(9, 38), "A", 0), ag(hm(9, 46), "A", 0)}, AgentFill,
			map[string]int{"A": 26}, map[string]int{"A": 21}, 25},
		{"a long tool run counts for its whole length",
			[]Event{ev(hm(9, 0), "A"), ag(hm(10, 0), "A", 50*time.Minute)}, AgentFill,
			map[string]int{"A": 60}, map[string]int{"A": 55}, 0},
		{"your prompt beats an agent on another ticket",
			[]Event{ev(hm(9, 0), "A"), ev(hm(9, 8), "A"), ag(hm(9, 5), "B", 0)}, AgentFill,
			map[string]int{"A": 13}, map[string]int{}, 0},
		{"agents on two tickets at once share the time",
			[]Event{ag(hm(9, 0), "A", 60*time.Minute), ag(hm(9, 0), "B", 60*time.Minute)}, AgentFill,
			map[string]int{"A": 32, "B": 33}, map[string]int{"A": 32, "B": 33}, 0}, // 65 shared minutes, by turns
		{"workday counts only between your first and last evidence",
			[]Event{ev(hm(9, 0), "A"), ev(hm(10, 0), "A"), ag(hm(9, 40), "A", 20*time.Minute), ag(hm(13, 0), "A", 60*time.Minute)}, AgentWorkday,
			map[string]int{"A": 35}, map[string]int{"A": 25}, 30},
		{"off ignores agents",
			[]Event{ev(hm(9, 0), "A"), ag(hm(9, 40), "A", 20*time.Minute)}, AgentOff,
			map[string]int{"A": 5}, map[string]int{}, 0},
		{"an agent with no ticket is unassigned time",
			[]Event{ag(hm(9, 10), "", 10*time.Minute)}, AgentFill,
			map[string]int{}, map[string]int{}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := For(day0, c.events, nil, Settings{IdleGap: 10, Tail: 5, Agent: c.mode})
			if got := totals(d); !reflect.DeepEqual(got, c.want) {
				t.Errorf("totals %v, want %v", got, c.want)
			}
			if got := agentTotals(d); !reflect.DeepEqual(got, c.agent) {
				t.Errorf("agent minutes %v, want %v", got, c.agent)
			}
			if d.Unassigned.Gaps != c.gaps {
				t.Errorf("gaps %d, want %d", d.Unassigned.Gaps, c.gaps)
			}
		})
	}
}

func TestNothingCountsAfterUntil(t *testing.T) {
	events := []Event{ev(hm(9, 0), "A"), ev(hm(9, 6), "A")}
	meetings := []Meeting{{From: hm(9, 30), To: hm(10, 0), Title: "standup"}, {From: hm(14, 0), To: hm(15, 0), Title: "review"}}
	cases := []struct {
		name     string
		until    time.Time
		want     map[string]int
		meetings int
		last     time.Time
	}{
		{"a future meeting does not count", hm(12, 0), map[string]int{"A": 11}, 30, hm(10, 0)},
		{"a meeting in progress counts through until's minute", hm(9, 45), map[string]int{"A": 11}, 16, hm(9, 46)},
		{"a tail is cut after until's minute", hm(9, 8), map[string]int{"A": 9}, 0, hm(9, 9)},
		{"zero until changes nothing", time.Time{}, map[string]int{"A": 11}, 90, hm(15, 0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Defaults
			s.Until = c.until
			d := For(day0, events, meetings, s)
			if got := totals(d); !reflect.DeepEqual(got, c.want) {
				t.Errorf("totals %v, want %v", got, c.want)
			}
			if d.Unassigned.Meetings != c.meetings {
				t.Errorf("meeting minutes %d, want %d", d.Unassigned.Meetings, c.meetings)
			}
			if last := d.Stretches[len(d.Stretches)-1].To; !last.Equal(c.last) {
				t.Errorf("the last stretch ends %s, want %s", last.Format("15:04"), c.last.Format("15:04"))
			}
			if last := d.Runs[len(d.Runs)-1].To; !last.Equal(c.last) {
				t.Errorf("the last run ends %s, want %s", last.Format("15:04"), c.last.Format("15:04"))
			}
		})
	}
	if !reflect.DeepEqual(For(day0, events, meetings, Defaults), For(day0, events, meetings, Settings{IdleGap: 10, Tail: 5, Until: day0.AddDate(0, 0, 2)})) {
		t.Error("an until after the day changes it")
	}
}
