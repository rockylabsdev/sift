package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rockylabsdev/sift/internal/derive"
	"github.com/rockylabsdev/sift/internal/tui"
)

func sentRuns(d derive.Day) (out []string) {
	for _, r := range d.Runs {
		out = append(out, r.Ticket+" "+r.From.Format("15:04")+"-"+r.To.Format("15:04"))
	}
	return out
}

// Time before the watermark is derived again on every load with the
// settings of the moment, so a change of idle gap redraws what was sent.
func TestSentTimeKeepsHowItWasSent(t *testing.T) {
	h := newHarness(t)
	h.jira(t)
	h.prompt(9, 0, h.repo)
	h.prompt(9, 14, h.repo)
	h.now = time.Date(2026, 11, 10, 10, 0, 0, 0, time.Local)
	code, out, _ := h.run("", "confirm")
	t.Logf("confirm: exit %d\n%s", code, out)

	r := &run{Env: Env{Getenv: func(k string) string { return h.env[k] }, Now: func() time.Time { return h.now }}}
	day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.Local)
	view := func() (string, []string) {
		l, err := r.loadAs("2026-11-10", nil, fetchLater)
		if err != nil {
			t.Fatal(err)
		}
		m := tui.New(source{r}, day, "")
		next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		next, _ = next.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
		return ansi.Strip(next.(tui.Model).View().Content), sentRuns(l.Before)
	}
	before, runsBefore := view()

	// the user changes the idle gap afterwards
	cfgPath := filepath.Join(h.env["XDG_CONFIG_HOME"], "sift", "config.json")
	b, _ := os.ReadFile(cfgPath)
	os.WriteFile(cfgPath, append(b[:len(b)-1], []byte(`,"idle_gap_minutes":15}`)...), 0o600)
	after, runsAfter := view()

	t.Logf("sent part before the setting change: %v", runsBefore)
	t.Logf("sent part after the setting change:  %v", runsAfter)
	if before != after {
		t.Logf("REVIEW FRAME BEFORE:\n%s", before)
		t.Logf("REVIEW FRAME AFTER:\n%s", after)
		t.Fatalf("the sent part of the day redrew after a settings change")
	}
}
