package tui

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rockylabsdev/sift/internal/draft"
	"github.com/rockylabsdev/sift/internal/evidence"
)

// conversations is a day of Claude Code conversations: s1 on PUN-142 in
// its own worktree, s3 on OPS-77 later.
func conversations() *fake {
	f := &fake{dec: map[string]draft.Decisions{}}
	f.say("s1", "/work/sift-pun-142", "PUN-142", hm(9, 0), hm(9, 50))
	f.say("s3", "/work/ops", "OPS-77", hm(11, 0), hm(11, 30))
	return f
}

func (f *fake) say(session, dir, t string, from, to time.Time) {
	for at := from; at.Before(to); at = at.Add(3 * time.Minute) {
		f.events = append(f.events, evidence.Event{At: at, Origin: evidence.Prompt, Kind: "prompt", Session: session, Dir: dir,
			Ticket: key(t), Found: &evidence.Found{Via: "branch", Value: "feature/" + t}})
	}
	sort.SliceStable(f.events, func(i, j int) bool { return f.events[i].At.Before(f.events[j].At) })
}

func lastLine(m Model) string {
	lines := strings.Split(frame(m), "\n")
	return lines[len(lines)-1]
}

func ticketsBySession(m Model) map[string]string {
	got := map[string]string{}
	for _, e := range m.in.Events {
		got[e.Session+" "+m.clockT(e.At)] = *e.Ticket
	}
	return got
}

func TestRetagOneConversation(t *testing.T) {
	f := conversations()
	m := sized(New(f, day0, ""), 130, 38)
	was := m.worklog("PUN-142").Minutes
	m = press(m, "r")
	if _, ok := m.mode.(typing); !ok {
		t.Fatalf("one conversation in the stretch goes straight to the ticket; mode %T", m.mode)
	}
	m = press(m, "p", "u", "n", "-", "1", "6", "0")
	fr := frame(m)
	for _, want := range []string{"PUN-142 · conversation in sift-pun-142 · 09:00–09:48", "count it for  PUN-160", "whole conversation"} {
		if !strings.Contains(fr, want) {
			t.Errorf("the retag panel lacks %q:\n%s", want, fr)
		}
	}
	m = press(m, "enter")
	question := fmt.Sprintf("Move %s of PUN-142 (conversation in sift-pun-142) to PUN-160?", dur(was))
	if got := lastLine(m); !strings.Contains(got, question) || !strings.Contains(got, "y saves · any other key cancels") || len(f.retags) != 0 {
		t.Fatalf("the check %q, %d retags saved; want %q and nothing saved yet", got, len(f.retags), question)
	}
	m = press(m, "y")
	if got := m.worklog("PUN-160").Minutes; got != was || m.worklog("PUN-142").Minutes != 0 || m.worklog("OPS-77").Minutes == 0 {
		t.Fatalf("after y: PUN-160 %dm, PUN-142 %dm, OPS-77 %dm; want PUN-160 %dm, PUN-142 none, OPS-77 kept", got, m.worklog("PUN-142").Minutes, m.worklog("OPS-77").Minutes, was)
	}
	fr = frame(m)
	if !strings.Contains(fr, "Moved "+dur(was)+" to PUN-160.") || !strings.Contains(fr, "PUN-160") || m.mode != nil {
		t.Fatalf("no message, or the draft does not show PUN-160:\n%s", fr)
	}
	if rt := f.retags[0]; rt.Session != "s1" || rt.Was != "PUN-142" || rt.Ticket != "PUN-160" || !rt.From.IsZero() {
		t.Fatalf("saved %+v", rt)
	}
}

func TestRetagChoosesTheConversation(t *testing.T) {
	f := conversations()
	f.say("s2", "/work/old-wt", "PUN-142", hm(9, 31), hm(9, 45))
	m := sized(press(New(f, day0, ""), "r"), 130, 38)
	fr := frame(m)
	for _, want := range []string{"›PUN-142 · conversation in sift-pun-142 · 09:00–09:48", " PUN-142 · conversation in old-wt · 09:31–09:43", "↑↓ choose"} {
		if !strings.Contains(fr, want) {
			t.Errorf("the chooser lacks %q:\n%s", want, fr)
		}
	}
	m = press(m, "down", "enter")
	if md, ok := m.mode.(typing); !ok || md.conv.session != "s2" {
		t.Fatalf("down, enter picks old-wt's conversation; mode %+v", m.mode)
	}
	m = press(m, "P", "U", "N", "-", "1", "6", "0", "enter", "y")
	for k, ticket := range ticketsBySession(m) {
		want := "PUN-142"
		switch {
		case strings.HasPrefix(k, "s2"):
			want = "PUN-160"
		case strings.HasPrefix(k, "s3"):
			want = "OPS-77"
		}
		if ticket != want {
			t.Errorf("%s is on %s, want %s", k, ticket, want)
		}
	}
}

func TestEscAtEachStepSavesNothing(t *testing.T) {
	f := conversations()
	f.say("s2", "/work/old-wt", "PUN-142", hm(9, 31), hm(9, 45))
	m := New(f, day0, "")
	for _, keys := range [][]string{
		{"r", "esc"},
		{"r", "enter", "esc"},
		{"r", "enter", "P", "U", "N", "-", "9", "enter", "esc"},
		{"r", "enter", "P", "U", "N", "-", "9", "enter", "n"},
	} {
		m = press(m, keys...)
		if len(f.retags) != 0 || m.mode != nil || m.msg != "Not retagged." || m.worklog("PUN-9").Minutes != 0 {
			t.Fatalf("%v: %d retags, mode %T, msg %q", keys, len(f.retags), m.mode, m.msg)
		}
	}
}

func TestRetagFromTheStretchOn(t *testing.T) {
	f := conversations()
	f.say("s1", "/work/sift-pun-142", "PUN-142", hm(8, 0), hm(8, 20))
	m := press(New(f, day0, ""), "down", "down", "r", "P", "U", "N", "-", "1", "6", "0", "ctrl+f")
	if fr := frame(sized(m, 130, 38)); !strings.Contains(fr, "its time from 09:00 on") {
		t.Fatalf("ctrl+f does not show the scope:\n%s", fr)
	}
	m = press(m, "enter", "y")
	for k, ticket := range ticketsBySession(m) {
		want := "PUN-160"
		switch {
		case strings.HasPrefix(k, "s3"):
			want = "OPS-77"
		case strings.HasPrefix(k, "s1 08"):
			want = "PUN-142"
		}
		if ticket != want {
			t.Errorf("%s is on %s, want %s", k, ticket, want)
		}
	}
	if !f.retags[0].From.Equal(hm(9, 0)) {
		t.Fatalf("saved %+v", f.retags[0])
	}
}

func TestUndoPutsTheTimeBack(t *testing.T) {
	f := conversations()
	m := sized(New(f, day0, ""), 130, 38)
	was := m.worklog("PUN-142").Minutes
	m = press(m, "r", "P", "U", "N", "-", "1", "6", "0", "enter", "y", "r")
	if got, want := lastLine(m), fmt.Sprintf("Put back %s of PUN-160 to PUN-142?", dur(was)); !strings.Contains(got, want) {
		t.Fatalf("r on retagged time: %q, want %q", got, want)
	}
	m = press(m, "y")
	if m.worklog("PUN-142").Minutes != was || m.worklog("PUN-160").Minutes != 0 || len(f.retags) != 0 {
		t.Fatalf("after undo PUN-142 %dm, PUN-160 %dm, %d retags", m.worklog("PUN-142").Minutes, m.worklog("PUN-160").Minutes, len(f.retags))
	}
	if !strings.Contains(lastLine(m), "Put back "+dur(was)+" to PUN-142.") {
		t.Fatalf("footer %q", lastLine(m))
	}
}

func TestUndoTakesBackOnlyTheLatestOfAChain(t *testing.T) {
	f := conversations()
	f.retags = []evidence.Retag{{Session: "s1", Was: "PUN-142", Ticket: "PUN-160", At: hm(19, 0)}, {Session: "s1", Was: "PUN-160", Ticket: "PUN-170", At: hm(19, 5)}}
	m := press(New(f, day0, ""), "r")
	if md, ok := m.mode.(undoing); !ok || md.rt.Ticket != "PUN-170" {
		t.Fatalf("mode %+v; want to put back PUN-170", m.mode)
	}
	m = press(m, "y")
	if m.worklog("PUN-160").Minutes == 0 || len(f.retags) != 1 || f.retags[0].Ticket != "PUN-160" {
		t.Fatalf("after undo %v; want the first retag kept", f.retags)
	}
}

func TestRetagRefusesWhatItCannotMove(t *testing.T) {
	f := conversations()
	f.dec["2026-11-10"] = draft.Decisions{Confirmed: &draft.Confirmed{Worklogs: []draft.Worklog{{Ticket: "PUN-142", Minutes: 60, Started: hm(9, 0)}}}}
	if m := press(New(f, day0, ""), "r"); m.msg != "Confirmed days do not change." || m.mode != nil {
		t.Fatalf("confirmed day: msg %q mode %T", m.msg, m.mode)
	}
	m := New(newFake(), day0, "")
	if m = press(m, "r"); !strings.Contains(m.msg, "no conversation id") || m.mode != nil {
		t.Fatalf("sessionless prompts: msg %q mode %T", m.msg, m.mode)
	}
	if m = press(m, "down", "r"); m.msg != "Retag is for logged work; t assigns meetings and gaps." || m.mode != nil {
		t.Fatalf("a meeting: msg %q mode %T", m.msg, m.mode)
	}
}

func TestRetagSuggestsTheDaysTicketsThenRecentOnes(t *testing.T) {
	f := conversations()
	f.recent = []string{"PUN-160", "OPS-77", "PUN-142", "PUN-99"}
	m := press(New(f, day0, ""), "r")
	if md := m.mode.(typing); strings.Join(md.sugg, ",") != "OPS-77,PUN-160,PUN-99" {
		t.Fatalf("suggestions %v; want the day's other ticket, then recent ones, never PUN-142", md.sugg)
	}
	if md := press(m, "tab", "tab").mode.(typing); md.field() != "PUN-160" {
		t.Fatalf("two tabs give %q", md.field())
	}
	m = press(m, "p", "tab", "tab")
	if md := m.mode.(typing); md.field() != "PUN-99" || len(md.shown()) != 2 {
		t.Fatalf("typing P filters to PUN-; two tabs give %q of %v", md.field(), md.shown())
	}
	m = press(m, "backspace", "backspace", "1", "enter")
	if md, ok := m.mode.(checking); !ok || md.rt.Ticket != "PUN-1" {
		t.Fatalf("backspace edits the suggestion in the field; mode %+v", m.mode)
	}
	m = press(New(f, day0, ""), "r", "P", "U", "N", "enter")
	if _, ok := m.mode.(typing); !ok || m.msg != "A ticket key looks like PUN-160." {
		t.Fatalf("enter on PUN: mode %T, msg %q", m.mode, m.msg)
	}
	if m = press(m, "-", "1", "4", "2", "enter"); m.msg != "This time is already on PUN-142." {
		t.Fatalf("enter on the ticket it is on: msg %q", m.msg)
	}
}

func TestARefreshWaitsForTheRetag(t *testing.T) {
	f := conversations()
	m := sized(press(New(f, day0, ""), "r"), 130, 38)
	for _, keys := range [][]string{{}, {"P", "U", "N", "-", "1", "6", "0", "enter"}} {
		m = press(m, keys...)
		f.prompts(14, 0, 30, "NEW-9")
		mode := fmt.Sprintf("%T", m.mode)
		if m = ticked(m); strings.Contains(frame(m), "NEW-9") || fmt.Sprintf("%T", m.mode) != mode {
			t.Fatalf("a refresh reloaded during %s:\n%s", mode, frame(m))
		}
	}
	if m = ticked(press(m, "n")); !strings.Contains(frame(m), "NEW-9") {
		t.Fatalf("after the retag a refresh still shows the old day")
	}
}

func TestRetaggedTimeIsMarked(t *testing.T) {
	f := conversations()
	m := press(New(f, day0, ""), "r", "P", "U", "N", "-", "1", "6", "0", "enter", "y")
	for _, size := range [][2]int{{130, 38}, {96, 30}} {
		fr := frame(sized(m, size[0], size[1]))
		var row, head string
		for _, l := range strings.Split(fr, "\n") {
			if strings.Contains(l, "│ time ") {
				head = l
			}
			if strings.Contains(l, "›09:00") {
				row = l
			}
		}
		if !strings.Contains(row, "← PUN-142") && !strings.Contains(row, "· moved") {
			t.Errorf("%dx%d: the stretch does not say its time was moved: %q", size[0], size[1], row)
		}
		end := func(l, cell string) int { return len([]rune(l[:strings.Index(l, cell)+len(cell)])) }
		if end(row, "53m") != end(head, "PUN-160") {
			t.Errorf("%dx%d: the mark broke the columns:\n%s\n%s", size[0], size[1], head, row)
		}
	}
	if fr := frame(sized(press(m, "e"), 130, 38)); !strings.Contains(fr, "retag PUN-142") {
		t.Fatalf("inspect does not say how PUN-160 was found:\n%s", fr)
	}
}
