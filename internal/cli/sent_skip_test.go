package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rockylabsdev/sift/internal/calendar"
	"github.com/rockylabsdev/sift/internal/draft"
	"github.com/rockylabsdev/sift/internal/tui"
)

// A meeting skipped before a send, and wholly before its watermark, stays
// a row in review, and x on it counts it again: a change to sent time that
// is not refused, and that redraws the sent part of the day.
func TestUnskippingASentMeetingLeavesSentTime(t *testing.T) {
	h := newHarness(t)
	h.jira(t)
	for m := 0; m < 30; m += 3 {
		h.prompt(8, m, h.repo)
	}
	day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.Local)
	h.now = time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local)
	r := &run{Env: Env{Getenv: func(k string) string { return h.env[k] }, Now: func() time.Time { return h.now }}, withCalendar: true}
	r.fetch = func(Config) (*calendar.Feed, error) { return feedOf(t, 1, day), nil } // sync 08:00-08:30
	s := source{r}
	s.Refresh(day)
	dir := filepath.Join(h.env["XDG_DATA_HOME"], "sift")
	if _, err := (draft.Store{Dir: dir}).Update("2026-11-10", func(d *draft.Decisions) error {
		d.Skip = []string{"sync-0@08:00"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	msg, err := s.Confirm(day)
	t.Logf("confirm: %q %v", msg, err)

	m := tui.New(s, day, "")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(tui.Model)
	before := ansi.Strip(m.View().Content)
	for _, k := range []string{"down", "x"} {
		var key tea.KeyPressMsg
		if k == "down" {
			key = tea.KeyPressMsg{Code: tea.KeyDown}
		} else {
			key = tea.KeyPressMsg{Code: 'x', Text: "x"}
		}
		next, _ = m.Update(key)
		m = next.(tui.Model)
	}
	after := ansi.Strip(m.View().Content)
	dec, _ := draft.Store{Dir: dir}.Load("2026-11-10")
	t.Logf("skip after x: %v, through %s", dec.Skip, dec.Through().Format("15:04"))
	in, _ := s.Load(day)
	for _, st := range in.Before.Stretches {
		t.Logf("sent part now: %s-%s kind %v meeting %q unassigned %d", st.From.Format("15:04"), st.To.Format("15:04"), st.Kind, st.Meeting, st.Unassigned)
	}
	box := func(f string) string { return strings.Join(strings.Split(f, "\n")[:16], "\n") }
	if len(dec.Skip) == 0 || box(before) != box(after) || !strings.Contains(m.View().Content, "Change it in Jira") {
		for _, l := range strings.Split(before, "\n")[:16] {
			t.Logf("BEFORE %s", l)
		}
		for _, l := range strings.Split(after, "\n")[:16] {
			t.Logf("AFTER  %s", l)
		}
		t.Fatalf("x on a skipped meeting before the 09:01 watermark was taken; want it refused and the sent part unchanged")
	}
}
