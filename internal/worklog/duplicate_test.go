package worklog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rockylabsdev/sift/internal/tracker"
)

func countPosts(f *tracker.Fake, key string) (n int) {
	for _, c := range f.Calls() {
		if c == "worklog "+key {
			n++
		}
	}
	return n
}

// markAttempted rewrites the file as if sift died after saving the
// attempt and before recording the answer.
func markAttempted(t *testing.T, s Store, ticket string) {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(s.Dir, FileName))
	var f file
	json.Unmarshal(b, &f)
	for i := range f.Worklogs {
		if f.Worklogs[i].Ticket == ticket {
			f.Worklogs[i].Attempted = true
		}
	}
	b, _ = json.Marshal(f)
	os.WriteFile(filepath.Join(s.Dir, FileName), b, 0o600)
}

func TestALostAnswerIsNotSentTwice(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	fake := &tracker.Fake{Lose: map[string]bool{"WEB-1": true}}
	confirm(t, s, day1(), false)
	if _, err := s.Sync(context.Background(), fake, false); err == nil {
		t.Fatal("a lost answer should be reported")
	}
	if w := list(t, s)[day+" WEB-1"]; !w.Attempted || w.State != Failed {
		t.Fatalf("after a lost answer: %+v", w)
	}
	fake.Lose = nil
	rep, err := s.Sync(context.Background(), fake, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := countPosts(fake, "WEB-1"); n != 1 {
		t.Fatalf("WEB-1 posted %d times, want once", n)
	}
	w := list(t, s)[day+" WEB-1"]
	if w.State != Synced || w.RemoteID != "W1" || w.Attempted {
		t.Fatalf("WEB-1 %+v; want synced as the tracker's W1", w)
	}
	if len(rep.Sent) != 2 || len(fake.Worklogs()) != 2 {
		t.Fatalf("sent %d, tracker holds %d; want 2 and 2", len(rep.Sent), len(fake.Worklogs()))
	}
}

func TestDiedAfterTheTrackerTookIt(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	fake := &tracker.Fake{}
	ws := confirm(t, s, day1()[:1], false)
	fake.PostWorklog(context.Background(), "WEB-1", tracker.Worklog{Started: ws[0].Started, TimeSpentSeconds: ws[0].Minutes * 60})
	markAttempted(t, s, "WEB-1")
	if _, err := s.Sync(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	if n := countPosts(fake, "WEB-1"); n != 1 {
		t.Fatalf("WEB-1 posted %d times, want only the one before the crash", n)
	}
	if w := list(t, s)[day+" WEB-1"]; w.State != Synced || w.RemoteID != "W1" {
		t.Fatalf("%+v", w)
	}
}

func TestDiedBeforeThePost(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	fake := &tracker.Fake{}
	confirm(t, s, day1()[:1], false)
	markAttempted(t, s, "WEB-1")
	if _, err := s.Sync(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	if n := countPosts(fake, "WEB-1"); n != 1 || len(fake.Worklogs()) != 1 {
		t.Fatalf("posted %d times; the tracker had nothing, so it is sent once", n)
	}
}

func TestUnsettledIsNotResentBlind(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	confirm(t, s, day1()[:1], false)
	markAttempted(t, s, "WEB-1")
	down := &tracker.Fake{Err: tracker.ErrUnreachable}
	if _, err := s.Sync(context.Background(), down, false); err == nil {
		t.Fatal("want an error")
	}
	for _, c := range down.Calls() {
		if c == "worklog WEB-1" {
			t.Fatal("posted without settling whether the tracker already had it")
		}
	}
	if w := list(t, s)[day+" WEB-1"]; !w.Attempted {
		t.Fatalf("the mark must survive: %+v", w)
	}
}

func TestARefusalClearsTheMark(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	fake := &tracker.Fake{PostErrs: map[string]error{"WEB-1": &tracker.Refusal{Status: 400, Message: "bad"}}}
	confirm(t, s, day1()[:1], false)
	s.Sync(context.Background(), fake, false)
	if w := list(t, s)[day+" WEB-1"]; w.Attempted || w.State != Failed {
		t.Fatalf("a refusal creates nothing, so the retry needs no check: %+v", w)
	}
}

func TestReconfirmKeepsAnUnsettledWorklog(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	confirm(t, s, day1()[:1], false)
	markAttempted(t, s, "WEB-1")
	changed := day1()[:1]
	changed[0].Minutes = 10
	got := confirm(t, s, changed, false)
	if got[0].Minutes != 90 || !got[0].Attempted {
		t.Fatalf("replacing it could orphan the copy the tracker may hold: %+v", got[0])
	}
}
