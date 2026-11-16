package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rockylabsdev/sift/internal/derive"
	"github.com/rockylabsdev/sift/internal/draft"
	"github.com/rockylabsdev/sift/internal/evidence"
)

var update = flag.Bool("update", false, "rewrite the golden frames")

var day0 = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)

func hm(h, m int) time.Time { return day0.Add(time.Duration(h*60+m) * time.Minute) }

// fake is a Source over one fixed day and an in-memory decisions store.
type fake struct {
	events   []evidence.Event
	meetings []derive.Meeting
	dec      map[string]draft.Decisions
	sent     map[string]Status
}

func key(s string) *string { return &s }

func newFake() *fake {
	f := &fake{dec: map[string]draft.Decisions{}}
	add := func(h, m int, t string) {
		e := evidence.Event{At: hm(h, m), Origin: evidence.Prompt, Kind: "prompt"}
		if t != "" {
			e.Ticket, e.Found = key(t), &evidence.Found{Via: "branch", Value: "feature/" + t}
		}
		f.events = append(f.events, e)
	}
	for m := 0; m < 50; m += 3 {
		add(9, m, "PUN-142")
	}
	for m := 52; m < 60; m += 4 {
		add(9, m, "OPS-77")
	}
	add(10, 40, "")
	for m := 0; m < 40; m += 5 {
		add(11, m, "OPS-77")
	}
	f.meetings = []derive.Meeting{{From: hm(10, 0), To: hm(10, 30), Title: "PUN-142 standup"}}
	return f
}

func (f *fake) Load(day time.Time) (Input, error) {
	var in []derive.Event
	var mine []evidence.Event
	for _, e := range f.events {
		t := ""
		if e.Ticket != nil {
			t = *e.Ticket
		}
		in = append(in, derive.Event{At: e.At, Ticket: t})
		if !e.At.Before(day) && e.At.Before(day.AddDate(0, 0, 1)) {
			mine = append(mine, e)
		}
	}
	var ms []derive.Meeting
	if day.Equal(day0) {
		ms = f.meetings
	}
	d := f.dec[day.Format("2006-01-02")]
	return Input{Start: day, Events: mine, Day: derive.For(day, in, ms, derive.Defaults), Decisions: d,
		IdleGap: 10, Meetings: "calendar: test", Today: day0.AddDate(0, 0, 2), Tracker: "Jira", Sent: f.sent}, nil
}

func (f *fake) Update(day time.Time, change func(*draft.Decisions) error) error {
	k := day.Format("2006-01-02")
	d := f.dec[k]
	if d.Confirmed != nil {
		return draft.ErrConfirmed
	}
	if err := change(&d); err != nil {
		return err
	}
	f.dec[k] = d
	return nil
}

func (f *fake) Tracker() string { return "Jira" }

func (f *fake) Confirm(day time.Time) (string, error) {
	in, _ := f.Load(day)
	d := draft.Of(in.Day, in.Decisions, 0)
	return "Confirmed.", f.Update(day, func(dec *draft.Decisions) error {
		dec.Confirmed = &draft.Confirmed{Worklogs: d.Sendable()}
		return nil
	})
}

func press(m Model, keys ...string) Model {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		default:
			msg = tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func sized(m Model, w, h int) Model {
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Model)
}

func frame(m Model) string { return ansi.Strip(m.View().Content) }

func golden(t *testing.T, name, got string) {
	t.Helper()
	var w, h int
	fmtScan(name, &w, &h)
	lines := strings.Split(got, "\n")
	if len(lines) > h {
		t.Errorf("%s: %d lines, taller than %d", name, len(lines), h)
	}
	for i, l := range lines {
		if n := ansi.StringWidth(l); n > w {
			t.Errorf("%s line %d is %d wide, over %d: %q", name, i+1, n, w, l)
		}
	}
	path := filepath.Join("testdata", name+".golden")
	if *update {
		os.MkdirAll("testdata", 0o755)
		os.WriteFile(path, []byte(got), 0o644)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/tui -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s changed; rerun with -update if intended\n--- got\n%s", name, got)
	}
}

func TestGoldenFrames(t *testing.T) {
	for _, size := range [][2]int{{96, 30}, {130, 38}} {
		w, h := size[0], size[1]
		sz := func(n string) string {
			return n + "-" + strings.ReplaceAll(strings.Trim(strings.ReplaceAll(fmtSize(w, h), " ", ""), ""), " ", "")
		}
		m := sized(New(newFake(), day0, ""), w, h)
		golden(t, sz("review"), frame(m))
		golden(t, sz("inspect"), frame(press(m, "e")))
		golden(t, sz("inspect-every-event"), frame(press(m, "e", "e")))
		golden(t, sz("meeting-assigned"), frame(press(m, "down", "t")))
		golden(t, sz("empty-day"), frame(press(m, "]")))
	}
}

func fmtSize(w, h int) string { return strings.Join([]string{itoa(w), itoa(h)}, "x") }

func itoa(n int) string {
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func TestAssignCyclesThroughTheTicketTheTitleNamesFirst(t *testing.T) {
	f := newFake()
	m := press(New(f, day0, ""), "down")
	if s, _ := m.selected(); s.Kind != derive.InMeet {
		t.Fatalf("selected %+v, want the meeting", s)
	}
	var got []string
	for i := 0; i < 3; i++ {
		m = press(m, "t")
		got = append(got, m.in.Decisions.Assign["meeting@10:00"])
	}
	if strings.Join(got, ",") != "PUN-142,OPS-77," {
		t.Fatalf("cycle %v; want the named ticket, the other ticket, then unassigned", got)
	}
}

func TestMinusStopsAtZero(t *testing.T) {
	m := New(newFake(), day0, "")
	for i := 0; i < 30; i++ {
		m = press(m, "-")
	}
	w := m.worklog("PUN-142")
	if w.Minutes != 0 || w.ByHand != -w.Counted {
		t.Fatalf("%+v", w)
	}
	m = press(m, "+")
	if m.worklog("PUN-142").Minutes != 5 {
		t.Fatalf("one + from the floor gives %dm, want 5m", m.worklog("PUN-142").Minutes)
	}
}

func TestAConfirmedDayRefusesChanges(t *testing.T) {
	f := newFake()
	f.dec["2026-11-10"] = draft.Decisions{Confirmed: &draft.Confirmed{Worklogs: []draft.Worklog{{Ticket: "PUN-142", Minutes: 60, Started: hm(9, 0)}}}}
	m := press(New(f, day0, ""), "+")
	if !strings.Contains(m.msg, "Confirmed days do not change") || m.dr.Total != 60 {
		t.Fatalf("msg %q total %d", m.msg, m.dr.Total)
	}
	fr := frame(sized(m, 120, 36))
	if !strings.Contains(fr, "· read-only") {
		t.Fatalf("the header does not say confirmed and read-only")
	}
	if !strings.Contains(fr, "confirmed, read-only") || strings.Contains(fr, "t assign") {
		t.Fatalf("a confirmed day offers keys it refuses:\n%s", fr)
	}
}

func TestOnlyAssignableStretchesAssign(t *testing.T) {
	m := press(New(newFake(), day0, ""), "t")
	if !strings.Contains(m.msg, "Only meetings") || len(m.in.Decisions.Assign) != 0 {
		t.Fatalf("msg %q, assign %v", m.msg, m.in.Decisions.Assign)
	}
}

func TestConfirmAsksFirst(t *testing.T) {
	f := newFake()
	m := sized(press(New(f, day0, ""), "c"), 120, 36)
	if !strings.Contains(frame(m), "Confirm Tue 10 Nov: 2 worklogs, 1h 40m to Jira?") {
		t.Fatalf("no question:\n%s", frame(m))
	}
	m = press(m, "n")
	if m.dr.Confirmed || m.msg != "Not confirmed." {
		t.Fatalf("any other key cancels: %+v %q", m.dr.Confirmed, m.msg)
	}
	m = press(m, "c", "y")
	if !m.dr.Confirmed || !strings.Contains(frame(m), "· read-only") {
		t.Fatalf("y confirms")
	}
	if m = press(m, "c"); m.msg != "Already confirmed." {
		t.Fatalf("msg %q", m.msg)
	}
}

// fmtScan reads the WxH at the end of a golden's name.
func fmtScan(name string, w, h *int) {
	i := strings.LastIndex(name, "-")
	ws, hs, _ := strings.Cut(name[i+1:], "x")
	for _, c := range ws {
		*w = *w*10 + int(c-'0')
	}
	for _, c := range hs {
		*h = *h*10 + int(c-'0')
	}
}

func TestAPastDaySaysSo(t *testing.T) {
	f := newFake()
	fr := frame(sized(New(f, day0, ""), 120, 36))
	if head := strings.SplitN(fr, "\n", 2)[0]; !strings.Contains(head, "Tue 10 Nov · 2 days ago") || !strings.Contains(head, "waiting for review") {
		t.Fatalf("a past unconfirmed day must say it is waiting:\n%s", strings.SplitN(fr, "\n", 2)[0])
	}
	f.dec["2026-11-10"] = draft.Decisions{Confirmed: &draft.Confirmed{At: hm(18, 5), Worklogs: []draft.Worklog{
		{Ticket: "PUN-142", Minutes: 60, Started: hm(9, 0)}, {Ticket: "OPS-77", Minutes: 30, Started: hm(9, 50)}}}}
	f.sent = map[string]Status{"PUN-142": {State: "synced"}, "OPS-77": {State: "failed", Error: "Jira answered 503"}}
	fr = frame(sized(New(f, day0, ""), 120, 36))
	for _, want := range []string{"confirmed Tue 18:05 · read-only", "3 confirmed", "sent to Jira", "! not sent yet", "Jira answered 503"} {
		if !strings.Contains(fr, want) {
			t.Errorf("missing %q:\n%s", want, fr)
		}
	}
}

func TestAgentsBridgingASilenceIsNotAWarning(t *testing.T) {
	f := newFake()
	f.events = nil
	add := func(h, m int, agent bool, span time.Duration) {
		e := evidence.Event{At: hm(h, m), Ticket: key("PUN-150"), Found: &evidence.Found{Via: "branch", Value: "f"}, Origin: evidence.Prompt}
		if agent {
			e.Origin, e.SpanMs, e.AgentID = evidence.Agent, span.Milliseconds(), "d4e1"
		}
		f.events = append(f.events, e)
	}
	add(13, 0, false, 0)
	for m := 4; m < 30; m += 3 {
		add(13, m, true, 2*time.Minute)
	}
	add(13, 30, false, 0)
	f.meetings = nil
	fr := frame(sized(press(New(f, day0, ""), "e"), 120, 36))
	if strings.Contains(fr, "idle gap; one more minute") || !strings.Contains(fr, "well inside the 10m idle gap") {
		t.Fatalf("agents held the stretch together; no fragile silence:\n%s", fr)
	}
	if !strings.Contains(fr, "1 subagent worked while you were not prompting") {
		t.Fatalf("the agent line is missing:\n%s", fr)
	}
}
