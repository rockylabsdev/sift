//go:build verify

// Credit verification for the daystrip prototype. Not a unit test suite: these
// checks explore the real derive, newModel, Update, total, eff and stretchBox
// code and look for any state where the draft would send different minutes
// than the screen credits. Run through .claude/skills/verify-daystrip-credit.
//
//	go test -tags verify -run 'Credit|Columns' ./cmd/prototype-daystrip
package main

import (
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Every key Update binds, except q and ctrl+c, which only quit.
var boundKeys = []tea.KeyPressMsg{
	{Code: tea.KeyLeft}, {Code: 'h', Text: "h"}, {Code: tea.KeyUp}, {Code: 'k', Text: "k"},
	{Code: tea.KeyRight}, {Code: 'l', Text: "l"}, {Code: tea.KeyDown}, {Code: 'j', Text: "j"},
	{Code: 'e', Text: "e"},
	{Code: '+', Text: "+"}, {Code: '=', Text: "="}, {Code: '-', Text: "-"},
	{Code: 't', Text: "t"}, {Code: tea.KeyEnter},
}

// adjustBound limits hand adjustments in the exhaustive search to ±adjustBound
// minutes per ticket, which keeps the state space finite.
const adjustBound = 10

// seedDay advances the package seed, so a second newModel in one process
// would build a different day. explore resets it to the value main starts with.
var initialSeed = seed

func press(m model, k tea.KeyPressMsg) model {
	next, _ := m.Update(k)
	return next.(model)
}

func stateKey(m model) string {
	return fmt.Sprint(m.cur, m.events, m.adjust, m.meetTo, m.gapTo)
}

// clone copies the slices Update writes through (model is passed by value,
// but adjust, meetTo and gapTo share backing arrays).
func clone(m model) model {
	m.adjust = append([]int(nil), m.adjust...)
	m.meetTo = append([]int(nil), m.meetTo...)
	m.gapTo = append([]int(nil), m.gapTo...)
	return m
}

// credited counts the minutes the day strip credits to ticket t: the minutes
// whose effective owner (eff) is t. This is what the counted bars show.
func credited(m model, t int) (n int) {
	for mm := 0; mm < dayMinutes; mm++ {
		if m.eff(mm) == t {
			n++
		}
	}
	return n
}

// checkCredit is property P1: each ticket's draft total equals the minutes the
// day strip credits to it plus its hand adjustment, floored at zero.
func checkCredit(m model) error {
	for t := range tickets {
		want := max(credited(m, t)+m.adjust[t], 0)
		if got := m.total(t); got != want {
			return fmt.Errorf("%s: draft total %s, but the day strip credits %s and the hand adjustment is %+dm (want %s)",
				tickets[t].key, dur(got), dur(credited(m, t)), m.adjust[t], dur(want))
		}
	}
	return nil
}

var durRe = regexp.MustCompile(`^(?:(\d+)h )?(\d+)m$`)

func parseDur(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "·" {
		return 0, nil
	}
	g := durRe.FindStringSubmatch(s)
	if g == nil {
		return 0, fmt.Errorf("not a duration: %q", s)
	}
	h, _ := strconv.Atoi("0" + g[1])
	mi, _ := strconv.Atoi(g[2])
	return h*60 + mi, nil
}

// checkColumns is property P2: in the stretches table as drawn, each ticket
// column adds up to the total printed under it (the promise in stretchBox's
// comment). It reads the rendered box, so it checks what the user sees.
func checkColumns(m model) error {
	const w, cw = 200, 9
	h := len(m.segments) + 10 // tall enough that no row scrolls away
	lines := m.stretchBox(w, h)
	iw := w - 2
	whatW := iw - 1 - 11 - 3 - cw*(len(tickets)+1) - 1
	start := 1 + 1 + 11 + 3 + whatW // box edge, then the row's fixed prefix
	cells := func(line string) ([]int, error) {
		r := []rune(ansi.Strip(line))
		var out []int
		for t := range tickets {
			n, err := parseDur(string(r[start+t*cw : start+(t+1)*cw]))
			if err != nil {
				return nil, err
			}
			out = append(out, n)
		}
		return out, nil
	}
	sum := make([]int, len(tickets))
	rows := lines[2 : 2+len(m.segments)] // top border, header, then one row per stretch
	for _, row := range rows {
		c, err := cells(row)
		if err != nil {
			return fmt.Errorf("reading row %q: %v", ansi.Strip(row), err)
		}
		for t := range sum {
			sum[t] += c[t]
		}
	}
	foot := lines[2+len(m.segments)+1] // after the rule
	if !strings.Contains(ansi.Strip(foot), "total") {
		return fmt.Errorf("footer not where expected: %q", ansi.Strip(foot))
	}
	tot, err := cells(foot)
	if err != nil {
		return err
	}
	for t := range tickets {
		if sum[t] != tot[t] {
			return fmt.Errorf("%s: column adds up to %s, total row shows %s", tickets[t].key, dur(sum[t]), dur(tot[t]))
		}
	}
	return nil
}

// explore visits every UI state of the seeded day reachable through the bound
// keys with each hand adjustment inside ±adjustBound, and calls check on each.
// It returns the first failure with the shortest key path that reaches it.
func explore(t *testing.T, check func(model) error) {
	type node struct {
		m    model
		path []string
	}
	seed = initialSeed
	start := newModel()
	if n := len(start.segments); n != 9 {
		t.Fatalf("seeded day has %d stretches, want the 9 the app shows", n)
	}
	seen := map[string]bool{stateKey(start): true}
	queue := []node{{start, nil}}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if err := check(n.m); err != nil {
			t.Fatalf("after keys %v: %v", n.path, err)
		}
		for _, k := range boundKeys {
			next := press(clone(n.m), k)
			if abs(float64(next.adjust[0])) > adjustBound || abs(float64(next.adjust[1])) > adjustBound || abs(float64(next.adjust[2])) > adjustBound {
				continue
			}
			if key := stateKey(next); !seen[key] {
				seen[key] = true
				queue = append(queue, node{next, append(append([]string(nil), n.path...), k.String())})
			}
		}
	}
	t.Logf("checked %d reachable states of the seeded day (hand adjustments within ±%dm)", len(seen), adjustBound)
}

// TestCreditSeededDay checks P1 in every reachable state of the seeded day.
func TestCreditSeededDay(t *testing.T) { explore(t, checkCredit) }

// TestColumnsSeededDay checks P2 in every reachable state of the seeded day.
// Known finding F2 as of the sync commit in the skill.
func TestColumnsSeededDay(t *testing.T) { explore(t, checkColumns) }

// genDay builds a day from r: 1-60 events at any minute on any ticket, and 0-3
// calendar meetings inside the day that may name a ticket. With overlap false,
// a meeting that would overlap an earlier one is dropped.
func genDay(r *rand.Rand, overlap bool) ([]event, []meeting) {
	ev := make([]event, 1+r.Intn(60))
	for i := range ev {
		ev[i] = event{r.Intn(dayMinutes), r.Intn(len(tickets))}
	}
	sortEvents(ev)
	ms := make([]meeting, r.Intn(4))
	for i := range ms {
		from := r.Intn(dayMinutes - 15)
		ms[i] = meeting{from: from, to: min(from+15+r.Intn(90), dayMinutes), title: fmt.Sprintf("meeting %d", i), suggest: none}
		if r.Intn(3) == 0 {
			ms[i].suggest = r.Intn(len(tickets))
		}
	}
	if overlap {
		return ev, ms
	}
	var keep []meeting
	for _, m := range ms {
		clash := false
		for _, k := range keep {
			clash = clash || (m.from < k.to && k.from < m.to)
		}
		if !clash {
			keep = append(keep, m)
		}
	}
	return ev, keep
}

func sortEvents(ev []event) {
	for i := 1; i < len(ev); i++ {
		for j := i; j > 0 && ev[j].min < ev[j-1].min; j-- {
			ev[j], ev[j-1] = ev[j-1], ev[j]
		}
	}
}

// runDay builds the model for a generated day exactly as main does (derive on
// the events, meetings from the package calendar), presses keys, and checks P1
// after every press. It restores the package calendar afterwards.
func runDay(ev []event, ms []meeting, keys []tea.KeyPressMsg) error {
	saved := meetings
	defer func() { meetings = saved }()
	meetings = ms
	built := modelFor(derive(ev))
	if err := checkCredit(built); err != nil {
		return fmt.Errorf("before any key: %v", err)
	}
	var pressed []string
	for _, k := range keys {
		built = press(built, k)
		pressed = append(pressed, k.String())
		if err := checkCredit(built); err != nil {
			return fmt.Errorf("after keys %v: %v", pressed, err)
		}
	}
	return nil
}

func keyNames(keys []tea.KeyPressMsg) (out []string) {
	for _, k := range keys {
		out = append(out, k.String())
	}
	return out
}

func describe(ev []event, ms []meeting, keys []tea.KeyPressMsg) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n  events:")
	for _, e := range ev {
		fmt.Fprintf(&b, " %s %s,", clock(e.min), tickets[e.ticket].key)
	}
	fmt.Fprintf(&b, "\n  meetings:")
	for _, mt := range ms {
		s := ""
		if mt.suggest != none {
			s = " (names " + tickets[mt.suggest].key + ")"
		}
		fmt.Fprintf(&b, " %s–%s%s,", clock(mt.from), clock(mt.to), s)
	}
	fmt.Fprintf(&b, "\n  keys: %v", keyNames(keys))
	return b.String()
}

// generated checks P1 on 20000 generated days, each driven by 40 random key
// presses. The seed is fixed, so a failure reproduces exactly.
func generated(t *testing.T, overlap bool) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		ev, ms := genDay(r, overlap)
		keys := make([]tea.KeyPressMsg, 40)
		for j := range keys {
			keys[j] = boundKeys[r.Intn(len(boundKeys))]
		}
		if runDay(ev, ms, keys) != nil {
			ev, ms, keys = shrink(ev, ms, keys)
			t.Fatalf("day %d, shrunk: %v%s", i, runDay(ev, ms, keys), describe(ev, ms, keys))
		}
	}
}

// TestCreditGeneratedDays checks P1 on generated days whose meetings never overlap.
func TestCreditGeneratedDays(t *testing.T) { generated(t, false) }

// TestCreditOverlappingMeetings checks P1 on generated days whose meetings may
// overlap. Known finding F1 as of the sync commit in the skill.
func TestCreditOverlappingMeetings(t *testing.T) { generated(t, true) }

// shrink greedily drops keys, events and meetings while the day still fails,
// so a counterexample reads as the few steps that matter.
func shrink(ev []event, ms []meeting, keys []tea.KeyPressMsg) ([]event, []meeting, []tea.KeyPressMsg) {
	fails := func(ev []event, ms []meeting, keys []tea.KeyPressMsg) bool { return runDay(ev, ms, keys) != nil }
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(keys); i++ {
			k := append(append([]tea.KeyPressMsg(nil), keys[:i]...), keys[i+1:]...)
			if fails(ev, ms, k) {
				keys, changed, i = k, true, i-1
			}
		}
		for i := 0; i < len(ev) && len(ev) > 1; i++ {
			e := append(append([]event(nil), ev[:i]...), ev[i+1:]...)
			if fails(e, ms, keys) {
				ev, changed, i = e, true, i-1
			}
		}
		for i := 0; i < len(ms); i++ {
			m := append(append([]meeting(nil), ms[:i]...), ms[i+1:]...)
			if fails(ev, m, keys) {
				ms, changed, i = m, true, i-1
			}
		}
	}
	return ev, ms, keys
}

// FuzzCreditGeneratedDays is the open-ended version of TestCreditGeneratedDays
// (overlap-free calendars): go test -tags verify
// -fuzz FuzzCreditGeneratedDays -fuzztime 60s ./cmd/prototype-daystrip
func FuzzCreditGeneratedDays(f *testing.F) {
	f.Add(int64(1), []byte("jjjt+t-"))
	f.Fuzz(func(t *testing.T, seed int64, ks []byte) {
		ev, ms := genDay(rand.New(rand.NewSource(seed)), false)
		var keys []tea.KeyPressMsg
		for _, b := range ks {
			keys = append(keys, boundKeys[int(b)%len(boundKeys)])
		}
		if runDay(ev, ms, keys) != nil {
			ev, ms, keys = shrink(ev, ms, keys)
			t.Fatalf("shrunk: %v%s", runDay(ev, ms, keys), describe(ev, ms, keys))
		}
	})
}
