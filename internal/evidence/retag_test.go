package evidence

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestApply(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 10, h, m, 0, 0, time.UTC) }
	ev := func(session, ticket string, h, m int) Event {
		e := Event{At: at(h, m), Session: session, Origin: Prompt}
		if ticket != "" {
			e.Ticket, e.Found = key(ticket), &Found{"branch", ticket}
		}
		return e
	}
	retag := func(session, was, ticket string) Retag {
		return Retag{Session: session, Was: was, Ticket: ticket, At: at(12, 0)}
	}
	from := func(rt Retag, h, m int) Retag { rt.From = at(h, m); return rt }

	for _, c := range []struct {
		name   string
		events []Event
		rs     []Retag
		want   []string // each event's ticket and via, "" for none
	}{
		{"match", []Event{ev("s1", "PUN-142", 9, 0)}, []Retag{retag("s1", "PUN-142", "PUN-160")}, []string{"PUN-160 retag PUN-142"}},
		{"other session", []Event{ev("s2", "PUN-142", 9, 0)}, []Retag{retag("s1", "PUN-142", "PUN-160")}, []string{"PUN-142 branch"}},
		{"a helper on its own ticket", []Event{ev("s1", "OPS-9", 9, 0)}, []Retag{retag("s1", "PUN-142", "PUN-160")}, []string{"OPS-9 branch"}},
		{"from", []Event{ev("s1", "PUN-142", 9, 0), ev("s1", "PUN-142", 9, 45), ev("s1", "PUN-142", 10, 0)},
			[]Retag{from(retag("s1", "PUN-142", "PUN-160"), 9, 45)}, []string{"PUN-142 branch", "PUN-160 retag PUN-142", "PUN-160 retag PUN-142"}},
		{"ticketless", []Event{ev("s1", "", 9, 0), ev("s1", "PUN-142", 9, 5)}, []Retag{retag("s1", "", "PUN-160")}, []string{"PUN-160 retag no ticket", "PUN-142 branch"}},
		{"chain", []Event{ev("s1", "PUN-142", 9, 0)}, []Retag{retag("s1", "PUN-142", "PUN-160"), retag("s1", "PUN-160", "PUN-170")}, []string{"PUN-170 retag PUN-160"}},
		{"no session", []Event{ev("", "PUN-142", 9, 0)}, []Retag{retag("s1", "PUN-142", "PUN-160")}, []string{"PUN-142 branch"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var before []string
			for _, e := range c.events {
				before = append(before, show(e))
			}
			for i, e := range Apply(c.events, c.rs) {
				if show(e) != c.want[i] {
					t.Errorf("event %d: %q, want %q", i, show(e), c.want[i])
				}
				if show(c.events[i]) != before[i] {
					t.Errorf("Apply changed its input to %q", show(c.events[i]))
				}
			}
		})
	}
}

func show(e Event) string {
	switch {
	case e.Ticket == nil:
		return ""
	case e.Found.Via == "retag":
		return *e.Ticket + " retag " + e.Found.Value
	}
	return *e.Ticket + " " + e.Found.Via
}

func TestRetagsAppendThenAll(t *testing.T) {
	s := Retags{Dir: t.TempDir()}
	a := Retag{Session: "s1", Was: "PUN-142", Ticket: "PUN-160", At: time.Date(2026, 11, 10, 9, 0, 0, 500, time.Local)}
	b := Retag{Session: "s1", Ticket: "PUN-170", From: time.Date(2026, 11, 10, 9, 45, 0, 0, time.Local), At: a.At.Add(time.Minute)}
	for _, rt := range []Retag{a, b} {
		if err := s.Append(rt); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Append(Retag{Ticket: "PUN-1"}); err == nil {
		t.Fatal("a retag with no session would move every session's time")
	}
	f, _ := os.OpenFile(filepath.Join(s.Dir, "retags.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("{torn\n")
	f.Close()
	got, problems, err := s.All()
	if err != nil || len(problems) != 1 || len(got) != 2 {
		t.Fatalf("got %+v problems %v err %v", got, problems, err)
	}
	if got[0].Was != "PUN-142" || !got[0].From.IsZero() || !got[1].From.Equal(b.From) || got[1].Was != "" || got[0].At.Nanosecond() != 0 {
		t.Fatalf("read back %+v", got)
	}
}

func TestRetagsRemoveTakesBackOnlyThatOne(t *testing.T) {
	s := Retags{Dir: t.TempDir()}
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	for i, rt := range []Retag{
		{Session: "s1", Was: "PUN-142", Ticket: "PUN-160", At: at},
		{Session: "s2", Was: "PUN-142", Ticket: "PUN-160", At: at},
		{Session: "s1", Was: "PUN-160", Ticket: "PUN-170", At: at.Add(time.Minute)},
	} {
		if err := s.Append(rt); err != nil {
			t.Fatal(i, err)
		}
		if i == 0 {
			f, _ := os.OpenFile(filepath.Join(s.Dir, "retags.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
			f.WriteString("{torn\n")
			f.Close()
		}
	}
	if err := s.Remove("s1", at.In(time.Local)); err != nil {
		t.Fatal(err)
	}
	got, problems, err := s.All()
	if err != nil || len(problems) != 1 || len(got) != 2 || got[0].Session != "s2" || got[1].Ticket != "PUN-170" {
		t.Fatalf("after removing s1's first retag: %+v, problems %v, err %v", got, problems, err)
	}
	if err := s.Remove("s1", at); err == nil {
		t.Fatal("removing a retag that is not there says nothing")
	}
	if entries, _ := os.ReadDir(s.Dir); len(entries) != 2 {
		t.Fatalf("left behind %v; want only the retags and the lock", entries)
	}
}
