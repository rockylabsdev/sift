// Package demo is a seeded fake day for trying sift without a tracker or
// a week of use: three tickets, hopping between sessions, a slow reading
// stretch, ticketless prompts, a prompt during a meeting, and two meetings.
package demo

import (
	"sort"
	"time"

	"github.com/rockylabsdev/sift/internal/derive"
	"github.com/rockylabsdev/sift/internal/evidence"
)

var branches = map[string]string{
	"PUN-142": "feature/PUN-142-side-lock",
	"OPS-77":  "ops/OPS-77-staging-certs",
	"PUN-150": "feature/PUN-150-stop-duration",
}

// Day is the demo's events and meetings on day.
func Day(day time.Time) ([]evidence.Event, []derive.Meeting) {
	seed := uint32(7)
	rnd := func(lo, hi int) int {
		seed = seed*1664525 + 1013904223
		return lo + int(seed>>16)%(hi-lo+1)
	}
	at := func(h, m int) time.Time { return day.Add(time.Duration(h*60+m) * time.Minute) }
	var evs []evidence.Event
	add := func(t time.Time, ticket string) {
		e := evidence.Event{V: evidence.Version, At: t, Source: "claude-code", Kind: "prompt", Origin: evidence.Prompt, Dir: "/home/you/src/app"}
		if ticket != "" {
			k := ticket
			e.Ticket, e.Found = &k, &evidence.Found{Via: "branch", Value: branches[ticket]}
		}
		evs = append(evs, e)
	}
	burst := func(t string, from, to time.Time, lo, hi int) {
		for m := from.Add(time.Duration(rnd(0, 2)) * time.Minute); m.Before(to); m = m.Add(time.Duration(rnd(lo, hi)) * time.Minute) {
			add(m, t)
		}
	}
	hop := func(a, b string, from, to time.Time, chunk int) {
		for m, i := from, 0; m.Before(to); m, i = m.Add(time.Duration(chunk)*time.Minute), i+1 {
			t := a
			if i%2 == 1 {
				t = b
			}
			end := m.Add(time.Duration(chunk) * time.Minute)
			if end.After(to) {
				end = to
			}
			burst(t, m, end, 1, 3)
		}
	}
	burst("PUN-142", at(8, 10), at(9, 20), 1, 4)
	hop("PUN-142", "OPS-77", at(9, 20), at(10, 0), 6)
	add(at(10, 20), "OPS-77") // a prompt during sprint planning
	burst("OPS-77", at(10, 50), at(11, 40), 2, 5)
	burst("", at(12, 5), at(12, 20), 2, 4) // a scratch session with no ticket
	burst("PUN-142", at(12, 30), at(13, 5), 1, 3)
	burst("PUN-142", at(13, 5), at(13, 55), 7, 9) // reading: slow prompts
	hop("PUN-150", "OPS-77", at(14, 35), at(15, 30), 9)
	burst("PUN-150", at(15, 55), at(17, 10), 1, 5)
	sort.Slice(evs, func(i, j int) bool { return evs[i].At.Before(evs[j].At) })
	return evs, []derive.Meeting{
		{From: at(10, 0), To: at(10, 45), Title: "Sprint planning"},
		{From: at(14, 0), To: at(14, 30), Title: "PUN-150 swarm"},
	}
}
