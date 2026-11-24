package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rockylabsdev/sift/internal/calendar"
	"github.com/rockylabsdev/sift/internal/derive"
	"github.com/rockylabsdev/sift/internal/draft"
)

// A past day sent in part, whose only time since the watermark is a
// meeting assigned to a ticket, has that meeting ready to send in review,
// but waiting() derives it without the calendar and lets it go.
func TestAPartlySentDayWithAssignedMeetingTimeWaits(t *testing.T) {
	h := newHarness(t)
	h.jira(t)
	day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.Local)
	h.prompt(9, 0, h.repo)
	h.now = time.Date(2026, 11, 10, 10, 0, 0, 0, time.Local)
	if code, out, _ := h.run("", "confirm"); code != 0 {
		t.Fatalf("confirm: %d %s", code, out)
	}
	dir := filepath.Join(h.env["XDG_DATA_HOME"], "sift")
	id := draft.ID(derive.Stretch{Kind: derive.InMeet, From: day.Add(14 * time.Hour)})
	if _, err := (draft.Store{Dir: dir}).Update("2026-11-10", func(d *draft.Decisions) error {
		d.Assign = map[string]string{id: "PUN-142"} // the 14:00 sync, given to PUN-142 in review
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	h.now = time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	r := &run{Env: Env{Getenv: func(k string) string { return h.env[k] }, Now: func() time.Time { return h.now }}, withCalendar: true}
	r.fetch = func(Config) (*calendar.Feed, error) { return feedOf(t, 7, day), nil } // syncs at 08:00, 09:00 ... 14:00
	s := source{r}
	s.Refresh(day)
	in, err := s.Load(day)
	if err != nil {
		t.Fatal(err)
	}
	ready := draft.Of(in.Day, in.Decisions, in.Round).Sendable()
	t.Logf("review of 2026-11-10 has ready to send: %+v", ready)
	// what the status hook runs: a fresh sift, as the shell starts it
	fresh := &run{Env: Env{Getenv: func(k string) string { return h.env[k] }, Now: func() time.Time { return h.now }}, withCalendar: true}
	fresh.fetch = r.fetch
	waiting, err := fresh.waiting()
	t.Logf("waiting: %v %v", waiting, err)
	_, _, data := h.envelope("status")
	t.Logf("sift status waiting: %v", data["waiting"])
	if len(ready) > 0 && len(waiting) == 0 {
		t.Fatalf("2026-11-10 has %dm ready to send in review but is not waiting", ready[0].Minutes)
	}
}
