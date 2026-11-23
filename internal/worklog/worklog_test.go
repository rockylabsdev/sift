package worklog

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rockylabsdev/sift/internal/tracker"
)

const day = "2026-11-10"

var nine = time.Date(2026, 11, 10, 9, 0, 0, 0, time.FixedZone("CET", 3600))

func day1() []Worklog {
	return []Worklog{
		{Ticket: "WEB-1", Started: nine, Minutes: 90, Comment: "review"},
		{Ticket: "WEB-2", Started: nine.Add(2 * time.Hour), Minutes: 45},
	}
}

func list(t *testing.T, s Store) map[string]Worklog {
	t.Helper()
	ws, err := s.List("0000-01-01", "9999-12-31")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Worklog{}
	for _, w := range ws {
		k := w.Date + " " + w.Ticket
		if _, dup := out[k]; dup {
			t.Fatalf("two worklogs for %s", k)
		}
		out[k] = w
	}
	return out
}

func confirm(t *testing.T, s Store, ws []Worklog, local bool) []Worklog {
	t.Helper()
	got, err := s.Confirm(day, 0, ws, local)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestConfirmTwiceDoesNotDuplicate(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	confirm(t, s, day1(), false)
	confirm(t, s, day1(), false)
	got := list(t, s)
	if len(got) != 2 || got[day+" WEB-1"].State != Pending || got[day+" WEB-1"].Minutes != 90 {
		t.Fatalf("after two confirms: %+v", got)
	}
}

func TestReconfirmReplacesUnsentAndKeepsSynced(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	fake := &tracker.Fake{}
	confirm(t, s, day1()[:1], false)
	if _, err := s.Sync(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	confirm(t, s, day1(), false)

	// The user moves WEB-2's time to WEB-3 and changes WEB-1.
	again := []Worklog{
		{Ticket: "WEB-1", Started: nine, Minutes: 120},
		{Ticket: "WEB-3", Started: nine, Minutes: 45},
	}
	got := confirm(t, s, again, false)
	if got[0].State != Synced || got[0].Minutes != 90 || got[1].State != Pending {
		t.Errorf("Confirm returned %+v; want WEB-1 as the tracker has it and WEB-3 pending", got)
	}
	all := list(t, s)
	if _, ok := all[day+" WEB-2"]; ok || len(all) != 2 {
		t.Errorf("an unsent worklog no longer confirmed was kept: %+v", all)
	}
	if _, err := s.Sync(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	if n := len(fake.Worklogs()); n != 2 {
		t.Errorf("tracker got %d worklogs, want 2 (WEB-1 once, WEB-3 once)", n)
	}
}

func TestSyncSendsWhatWasConfirmed(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	fake := &tracker.Fake{}
	confirm(t, s, day1(), false)
	rep, err := s.Sync(context.Background(), fake, false)
	if err != nil || len(rep.Sent) != 2 || len(rep.Remaining) != 0 {
		t.Fatalf("Sync = %+v, %v", rep, err)
	}
	posted := fake.Worklogs()
	want := tracker.PostedWorklog{Key: "WEB-1", ID: "W1", Worklog: tracker.Worklog{Started: nine, TimeSpentSeconds: 5400, Comment: "review"}}
	if len(posted) != 2 || posted[0].Key != want.Key || !posted[0].Worklog.Started.Equal(nine) ||
		posted[0].Worklog.TimeSpentSeconds != 5400 || posted[0].Worklog.Comment != "review" {
		t.Errorf("posted %+v, want first %+v", posted, want)
	}
	w := list(t, s)[day+" WEB-1"]
	if w.State != Synced || w.RemoteID != "W1" || w.SyncedAt.IsZero() {
		t.Errorf("WEB-1 on disk = %+v", w)
	}
}

func TestNoDoubleSend(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	fake := &tracker.Fake{}
	confirm(t, s, day1(), false)
	for range 2 {
		if _, err := s.Sync(context.Background(), fake, false); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(fake.Worklogs()); n != 2 {
		t.Errorf("two syncs posted %d worklogs, want 2", n)
	}
}

// slow holds each POST long enough for a second sync to start meanwhile.
type slow struct{ *tracker.Fake }

func (c slow) PostWorklog(ctx context.Context, key string, w tracker.Worklog) (string, error) {
	time.Sleep(100 * time.Millisecond)
	return c.Fake.PostWorklog(ctx, key, w)
}

func TestConcurrentSyncsSendOnce(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	fake := &tracker.Fake{}
	confirm(t, s, day1(), false)
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if _, err := s.Sync(context.Background(), slow{fake}, false); err != nil && !errors.Is(err, ErrBusy) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := len(fake.Worklogs()); n != 2 {
		t.Errorf("three concurrent syncs posted %d worklogs, want 2", n)
	}
}

func TestFailedSendIsKeptAndRetried(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	fake := &tracker.Fake{PostErrs: map[string]error{"WEB-1": &tracker.Refusal{Status: 503, Message: "down for maintenance"}}}
	confirm(t, s, day1(), false)

	rep, err := s.Sync(context.Background(), fake, false)
	var refused *tracker.Refusal
	if !errors.As(err, &refused) || refused.Status != 503 {
		t.Fatalf("err = %v, want the 503", err)
	}
	if len(rep.Sent) != 1 || rep.Sent[0].Ticket != "WEB-2" {
		t.Errorf("a refusal stopped the run: sent %+v", rep.Sent)
	}
	w := list(t, s)[day+" WEB-1"]
	if w.State != Failed || w.Error == "" || len(rep.Remaining) != 1 {
		t.Fatalf("WEB-1 = %+v, remaining %+v; want failed with its reason", w, rep.Remaining)
	}

	fake.PostErrs = nil
	rep, err = s.Sync(context.Background(), fake, false)
	if err != nil || len(rep.Sent) != 1 || len(rep.Remaining) != 0 {
		t.Fatalf("retry = %+v, %v", rep, err)
	}
	if w := list(t, s)[day+" WEB-1"]; w.State != Synced || w.Error != "" {
		t.Errorf("after retry WEB-1 = %+v", w)
	}
	want := []string{"worklog WEB-1", "worklog WEB-2", "worklog WEB-1"}
	if got := fake.Calls(); len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func TestUnreachableStopsTheRun(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	fake := &tracker.Fake{Err: tracker.ErrUnreachable}
	confirm(t, s, day1(), false)
	rep, err := s.Sync(context.Background(), fake, false)
	if !errors.Is(err, tracker.ErrUnreachable) || len(fake.Calls()) != 1 {
		t.Fatalf("err = %v, calls %v; want one try then stop", err, fake.Calls())
	}
	if got := list(t, s); got[day+" WEB-1"].State != Failed || got[day+" WEB-2"].State != Pending || len(rep.Remaining) != 2 {
		t.Errorf("after an unreachable tracker: %+v", got)
	}
}

func TestUnauthorizedLeavesPending(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	fake := &tracker.Fake{Err: &tracker.Refusal{Status: 401}}
	confirm(t, s, day1(), false)
	_, err := s.Sync(context.Background(), fake, false)
	if !errors.Is(err, tracker.ErrUnauthorized) || len(fake.Calls()) != 1 {
		t.Fatalf("err = %v, calls %v", err, fake.Calls())
	}
	if w := list(t, s)[day+" WEB-1"]; w.State != Pending {
		t.Errorf("a 401 is about the credentials, not the worklog: WEB-1 = %+v", w)
	}
}

func TestLocalMode(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	fake := &tracker.Fake{}
	got := confirm(t, s, day1(), true)
	if got[0].State != Local || got[1].State != Local {
		t.Fatalf("Confirm with no tracker = %+v, want local", got)
	}
	rep, err := s.Sync(context.Background(), fake, true)
	if err != nil || len(rep.Sent)+len(rep.Remaining) != 0 {
		t.Fatalf("Sync with no tracker = %+v, %v", rep, err)
	}
	// A tracker set up later is never owed the local ones.
	if _, err := s.Sync(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("local worklogs were sent: %v", calls)
	}
}

func TestOwedWithoutClient(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	confirm(t, s, day1(), false)
	rep, err := s.Sync(context.Background(), nil, false)
	if !errors.Is(err, tracker.ErrNotConfigured) || len(rep.Remaining) != 2 {
		t.Errorf("Sync with no client = %+v, %v", rep, err)
	}
	if _, err := (Store{Dir: t.TempDir()}).Sync(context.Background(), nil, false); err != nil {
		t.Errorf("nothing owed needs no client: %v", err)
	}
}

func TestConfirmRefusesBadWorklogs(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for name, ws := range map[string][]Worklog{
		"other day":  {{Date: "2026-11-11", Ticket: "WEB-1", Started: nine, Minutes: 5}},
		"no ticket":  {{Started: nine, Minutes: 5}},
		"no minutes": {{Ticket: "WEB-1", Started: nine}},
		"no start":   {{Ticket: "WEB-1", Minutes: 5}},
		"twice":      {{Ticket: "WEB-1", Started: nine, Minutes: 5}, {Ticket: "WEB-1", Started: nine, Minutes: 5}},
	} {
		if _, err := s.Confirm(day, 0, ws, false); err == nil {
			t.Errorf("%s: Confirm took it", name)
		}
	}
	if len(list(t, s)) != 0 {
		t.Error("a refused confirm wrote worklogs")
	}
}

func TestListRange(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, d := range []string{"2026-11-09", "2026-11-10", "2026-11-11"} {
		if _, err := s.Confirm(d, 0, []Worklog{{Ticket: "WEB-1", Started: nine, Minutes: 5}}, true); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List("2026-11-10", "2026-11-11")
	if err != nil || len(got) != 2 || got[0].Date != "2026-11-10" || got[1].Date != "2026-11-11" {
		t.Errorf("List = %+v, %v", got, err)
	}
}

func TestADaySentInPartsKeepsEachPart(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	fake := &tracker.Fake{}
	if _, err := s.Confirm(day, 0, []Worklog{{Ticket: "WEB-1", Started: nine, Minutes: 30}}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	later := nine.Add(3 * time.Hour)
	for range 2 {
		if _, err := s.Confirm(day, 1, []Worklog{{Ticket: "WEB-1", Started: later, Minutes: 20}}, false); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := s.List(day, day)
	if len(all) != 2 || all[0].Part != 0 || all[0].State != Synced || all[1].Part != 1 || all[1].State != Pending {
		t.Fatalf("parts %+v; want part 0 synced and one pending part 1", all)
	}
	rep, err := s.Sync(context.Background(), fake, false)
	if err != nil || len(rep.Sent) != 1 || len(fake.Worklogs()) != 2 || fake.Worklogs()[1].Worklog.TimeSpentSeconds != 1200 {
		t.Fatalf("second sync sent %+v (%v); tracker holds %+v", rep.Sent, err, fake.Worklogs())
	}
}
