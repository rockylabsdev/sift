package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rockylabsdev/sift/internal/calendar"
)

type harness struct {
	t    *testing.T
	env  map[string]string
	now  time.Time
	repo string
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, env: map[string]string{"XDG_DATA_HOME": t.TempDir(), "XDG_CONFIG_HOME": t.TempDir(), "HOME": t.TempDir()}}
	h.now = time.Date(2026, 11, 10, 23, 0, 0, 0, time.Local)
	h.repo = t.TempDir()
	out, err := exec.Command("git", "-C", h.repo, "init", "-q", "-b", "feature/PUN-142-side-lock").CombinedOutput()
	if err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	return h
}

func (h *harness) run(stdin string, args ...string) (code int, stdout, stderr string) {
	var o, e bytes.Buffer
	env := Env{
		Getenv: func(k string) string { return h.env[k] },
		Stdin:  strings.NewReader(stdin),
		Stdout: &o, Stderr: &e,
		Now: func() time.Time { return h.now },
	}
	return Run(args, env), o.String(), e.String()
}

// prompt records a prompt the way the plugin's hook does, at h:m.
func (h *harness) prompt(hh, mm int, dir string) {
	h.t.Helper()
	defer func(now time.Time) { h.now = now }(h.now)
	h.now = time.Date(2026, 11, 10, hh, mm, 0, 0, time.Local)
	in, _ := json.Marshal(map[string]string{"cwd": dir, "hook_event_name": "UserPromptSubmit"})
	code, out, errOut := h.run(string(in), "record", "--hook")
	if code != 0 || out != "" || errOut != "" {
		h.t.Fatalf("record --hook: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func (h *harness) envelope(args ...string) (int, envelope, map[string]any) {
	h.t.Helper()
	code, out, _ := h.run("", append(args, "--json")...)
	var e envelope
	var raw struct{ Data map[string]any }
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		h.t.Fatalf("not an envelope: %v\n%s", err, out)
	}
	json.Unmarshal([]byte(out), &raw)
	return code, e, raw.Data
}

func TestHookPromptsBecomeADraft(t *testing.T) {
	h := newHarness(t)
	ops := filepath.Join(h.repo, "ops")
	os.MkdirAll(ops, 0o700)
	os.WriteFile(filepath.Join(ops, ".sift"), []byte("OPS-77\n"), 0o600)

	h.prompt(9, 0, h.repo)
	h.prompt(9, 6, ops)
	h.prompt(10, 0, h.repo)

	h.now = time.Date(2026, 11, 10, 18, 0, 0, 0, time.Local)
	code, e, data := h.envelope("draft")
	if code != 0 || !e.OK || e.Outcome != "ok" || e.V != ContractVersion {
		t.Fatalf("draft: exit %d, %+v", code, e)
	}
	got := map[string]float64{}
	for _, w := range data["worklogs"].([]any) {
		w := w.(map[string]any)
		got[w["ticket"].(string)] = w["minutes"].(float64)
	}
	if got["PUN-142"] != 8 || got["OPS-77"] != 8 || data["total"].(float64) != 16 {
		t.Fatalf("worklogs %v total %v; want PUN-142 8m (3 + 5), OPS-77 8m", got, data["total"])
	}
	if gaps := data["unassigned"].(map[string]any)["gaps"].(float64); gaps != 49 {
		t.Errorf("gaps = %vm, want 49m (09:11 to 10:00)", gaps)
	}

	code, out, _ := h.run("", "draft")
	if code != 0 || !strings.Contains(out, "PUN-142") || !strings.Contains(out, "16m   2 worklogs") {
		t.Errorf("human draft:\n%s", out)
	}
}

func TestEventsKeepWhereTheTicketCameFrom(t *testing.T) {
	h := newHarness(t)
	h.prompt(9, 0, h.repo)
	_, e, data := h.envelope("events")
	evs := data["events"].([]any)
	if !e.OK || len(evs) != 1 {
		t.Fatalf("%+v %v", e, data)
	}
	ev := evs[0].(map[string]any)
	found := ev["found"].(map[string]any)
	if ev["ticket"] != "PUN-142" || found["via"] != "branch" || found["value"] != "feature/PUN-142-side-lock" || ev["origin"] != "prompt" || ev["source"] != "claude-code" {
		t.Errorf("event %v", ev)
	}
}

func TestAnAgentsShellIsNotEvidence(t *testing.T) {
	h := newHarness(t)
	h.env["CLAUDECODE"] = "1"
	code, e, data := h.envelope("record", "--cwd", h.repo)
	if code != 0 || data["recorded"] != false || len(e.Events) != 1 {
		t.Fatalf("exit %d %+v", code, e)
	}
	_, _, data = h.envelope("events")
	if n := len(data["events"].([]any)); n != 0 {
		t.Fatalf("an agent's record left %d events", n)
	}
}

func TestByHandIsEvidence(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run("", "record", "--cwd", h.repo)
	if code != 0 || !strings.Contains(out, "PUN-142 (branch)") {
		t.Fatalf("exit %d: %q", code, out)
	}
}

func TestTheHookNeverFailsThePrompt(t *testing.T) {
	h := newHarness(t)
	delete(h.env, "XDG_DATA_HOME")
	delete(h.env, "HOME")
	code, out, errOut := h.run("{not json", "record", "--hook")
	if code != 0 || out != "" || errOut == "" {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, errOut)
	}
	code, out, _ = h.run(`{"cwd":"/nowhere"}`, "record", "--hook")
	if code != 0 || out != "" {
		t.Fatalf("no data dir: exit %d stdout %q", code, out)
	}
}

func TestUsageNeverExits2(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"frobnicate"}, {"draft", "--date", "tuesday"}, {"draft", "--md", "--json"}, {"events", "extra"}} {
		code, e, _ := h.envelope(args...)
		if code != 64 || e.OK || e.Outcome != "usage" || e.Error == nil {
			t.Errorf("%v: exit %d %+v", args, code, e)
		}
	}
}

func TestRoundingAndTheIdleGapComeFromConfig(t *testing.T) {
	h := newHarness(t)
	os.MkdirAll(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift"), 0o700)
	os.WriteFile(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift", "config.json"), []byte(`{"idle_gap_minutes":15,"round_minutes":15}`), 0o600)
	h.prompt(9, 0, h.repo)
	h.prompt(9, 14, h.repo)
	_, _, data := h.envelope("draft")
	w := data["worklogs"].([]any)[0].(map[string]any)
	if w["counted"].(float64) != 19 || w["minutes"].(float64) != 15 {
		t.Fatalf("worklog %v; want 19m counted with a 15m idle gap, rounded to 15m", w)
	}

	os.WriteFile(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift", "config.json"), []byte(`{"round_minutes":7}`), 0o600)
	if code, e, _ := h.envelope("draft"); code != 1 || e.OK {
		t.Fatalf("a bad setting: exit %d %+v", code, e)
	}
}

func TestDraftMarkdown(t *testing.T) {
	h := newHarness(t)
	h.prompt(9, 0, h.repo)
	h.env["SIFT_JSON"] = "1"
	code, out, _ := h.run("", "draft", "--md")
	if code != 0 || !strings.HasPrefix(out, "# Draft · Tue 10 Nov 2026") || !strings.Contains(out, "| PUN-142 | 5m | 09:00 |") {
		t.Fatalf("a format asked for by name prints raw, even with SIFT_JSON:\n%s", out)
	}
}

func TestProjectsSettingReachesTheHook(t *testing.T) {
	h := newHarness(t)
	exec.Command("git", "-C", h.repo, "checkout", "-q", "-b", "fix-2/pun-150-stop").Run()
	os.MkdirAll(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift"), 0o700)
	os.WriteFile(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift", "config.json"), []byte(`{"projects":["PUN"]}`), 0o600)
	h.prompt(9, 0, h.repo)
	_, _, data := h.envelope("events")
	if ev := data["events"].([]any)[0].(map[string]any); ev["ticket"] != "PUN-150" {
		t.Fatalf("ticket %v, want PUN-150", ev["ticket"])
	}
}

func TestABrokenConfigStillRecords(t *testing.T) {
	h := newHarness(t)
	os.MkdirAll(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift"), 0o700)
	os.WriteFile(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift", "config.json"), []byte(`{broken`), 0o600)
	in := `{"cwd":"` + h.repo + `"}`
	if code, out, errOut := h.run(in, "record", "--hook"); code != 0 || out != "" || !strings.Contains(errOut, "ignoring projects") {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, errOut)
	}
	os.Remove(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift", "config.json"))
	if _, _, data := h.envelope("events"); len(data["events"].([]any)) != 1 {
		t.Fatalf("the prompt was not recorded")
	}
}

func TestABlockOwnsItsTime(t *testing.T) {
	h := newHarness(t)
	h.prompt(9, 0, h.repo)
	h.prompt(9, 5, h.repo)
	if code, out, _ := h.run("", "block", "OPS-77", "9:05-10:00", "--date", "2026-11-10"); code != 0 || !strings.Contains(out, "Added OPS-77 09:05–10:00 (55m)") {
		t.Fatalf("exit %d: %q", code, out)
	}
	_, _, data := h.envelope("draft")
	got := map[string]float64{}
	for _, w := range data["worklogs"].([]any) {
		w := w.(map[string]any)
		got[w["ticket"].(string)] = w["minutes"].(float64)
	}
	if got["PUN-142"] != 5 || got["OPS-77"] != 55 {
		t.Fatalf("worklogs %v; want PUN-142 5m (its tail is under the block), OPS-77 55m", got)
	}
	if code, _, _ := h.run("", "block", "ops-77", "09:05-10:00", "--remove", "--date", "2026-11-10"); code != 0 {
		t.Fatalf("remove: exit %d", code)
	}
	if code, _, _ := h.run("", "block", "OPS-77", "10:00-09:00"); code != 64 {
		t.Fatalf("a backwards block: exit %d", code)
	}
}

func TestTodayCountsNothingAfterNow(t *testing.T) {
	h := newHarness(t)
	h.prompt(9, 0, h.repo)
	h.now = time.Date(2026, 11, 10, 9, 2, 0, 0, time.Local)
	if _, _, data := h.envelope("draft"); data["total"].(float64) != 3 {
		t.Fatalf("at 09:02 a 09:00 prompt counts %vm, want 3m, through the minute now is in", data["total"])
	}
	h.now = time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	if _, _, data := h.envelope("draft", "--date", "2026-11-10"); data["total"].(float64) != 5 {
		t.Fatalf("the next day the prompt counts %vm, want its 5m tail", data["total"])
	}
}

// feedOf is a feed of n half-hour meetings with a colleague on day, from 08:00.
func feedOf(t *testing.T, n int, day time.Time) *calendar.Feed {
	t.Helper()
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n"
	for i := 0; i < n; i++ {
		from := day.Add(time.Duration(8+i) * time.Hour)
		ics += fmt.Sprintf("BEGIN:VEVENT\r\nUID:sync-%d\r\nSUMMARY:sync\r\nDTSTART:%s\r\nDTEND:%s\r\nORGANIZER:mailto:pal@example.com\r\nEND:VEVENT\r\n",
			i, from.Format("20060102T150405"), from.Add(30*time.Minute).Format("20060102T150405"))
	}
	f, err := calendar.Parse([]byte(ics+"END:VCALENDAR\r\n"), "me@example.com", day.Location())
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestTheReviewRefetchesTodaysCalendarEveryFiveMinutes(t *testing.T) {
	h := newHarness(t)
	h.now = time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local)
	today, yesterday := time.Date(2026, 11, 10, 0, 0, 0, 0, time.Local), time.Date(2026, 11, 9, 0, 0, 0, 0, time.Local)
	fetches := 0
	r := &run{Env: Env{Getenv: func(k string) string { return h.env[k] }, Now: func() time.Time { return h.now }}, withCalendar: true}
	r.fetch = func(Config) (*calendar.Feed, error) {
		fetches++
		return feedOf(t, fetches, today), nil
	}
	s := source{r}
	meetings := func(day time.Time) string {
		t.Helper()
		in, err := s.Load(day)
		if err != nil {
			t.Fatal(err)
		}
		return in.Meetings
	}
	if got := meetings(today); fetches != 0 || got != "calendar: loading" {
		t.Fatalf("the first load fetched %d times and says %q; want no fetch, and loading", fetches, got)
	}
	s.Refresh(today)
	if got := meetings(today); fetches != 1 || got != "calendar: 1 meeting" {
		t.Fatalf("after the first refresh, %d fetches and %q", fetches, got)
	}
	h.now = h.now.Add(4 * time.Minute)
	s.Refresh(today)
	if got := meetings(today); fetches != 1 || got != "calendar: 1 meeting" {
		t.Fatalf("four minutes on, %d fetches and %q; want the first fetch kept", fetches, got)
	}
	h.now = h.now.Add(time.Minute)
	done := make(chan struct{})
	go func() { s.Refresh(today); close(done) }()
	for i := 0; i < 3; i++ {
		meetings(today)
	}
	<-done
	if got := meetings(today); fetches != 2 || got != "calendar: 2 meetings" {
		t.Fatalf("five minutes on, %d fetches and %q; want a second fetch and its new meeting", fetches, got)
	}
	if got := meetings(yesterday); got != "calendar: 0 meetings" {
		t.Fatalf("yesterday says %q", got)
	}
	h.now = h.now.Add(time.Hour)
	s.Refresh(yesterday)
	if fetches != 2 {
		t.Fatalf("%d fetches; a past day reads the run's feed and never refetches it", fetches)
	}
}

func TestTheReviewFetchesTheFeedOnceAndNeverOnTheUIGoroutine(t *testing.T) {
	h := newHarness(t)
	h.now = time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local)
	today := time.Date(2026, 11, 10, 0, 0, 0, 0, time.Local)
	var mu sync.Mutex
	fetches := 0
	release := make(chan struct{})
	r := &run{Env: Env{Getenv: func(k string) string { return h.env[k] }, Now: func() time.Time { return h.now }}, withCalendar: true}
	r.fetch = func(Config) (*calendar.Feed, error) {
		mu.Lock()
		fetches++
		mu.Unlock()
		<-release
		return feedOf(t, 1, today.AddDate(0, 0, -2)), nil
	}
	s := source{r}
	go s.Refresh(today)
	for i := 0; i < 3; i++ {
		loaded := make(chan string, 1)
		go func(day time.Time) {
			in, _ := s.Load(day)
			loaded <- in.Meetings
		}(today.AddDate(0, 0, -i))
		select {
		case got := <-loaded:
			if got != "calendar: loading" {
				t.Errorf("day -%d says %q while the feed downloads", i, got)
			}
		case <-time.After(2 * time.Second):
			close(release)
			t.Fatalf("loading day -%d waited for the calendar download", i)
		}
	}
	close(release)
	for i := 0; i < 3; i++ {
		s.Refresh(today.AddDate(0, 0, -i))
	}
	if fetches != 1 {
		t.Fatalf("three days downloaded the feed %d times; want once per run", fetches)
	}
	if in, _ := s.Load(today.AddDate(0, 0, -2)); in.Meetings != "calendar: 1 meeting" || in.Pending {
		t.Fatalf("after the fetch, two days back says %q, pending %v", in.Meetings, in.Pending)
	}
}
