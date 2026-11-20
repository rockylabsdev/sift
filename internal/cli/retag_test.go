package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rockylabsdev/sift/internal/draft"
	"github.com/rockylabsdev/sift/internal/evidence"
	"github.com/rockylabsdev/sift/internal/tui"
)

// say types a prompt in a session at hh:mm and returns what the hook
// added to Claude's context.
func (h *harness) say(hh, mm int, session, dir, prompt string) string {
	h.t.Helper()
	defer func(now time.Time) { h.now = now }(h.now)
	h.now = time.Date(2026, 11, 10, hh, mm, 0, 0, time.Local)
	in := map[string]any{"hook_event_name": "UserPromptSubmit", "cwd": dir, "prompt": prompt}
	if session != "" {
		in["session_id"] = session
	}
	b, _ := json.Marshal(in)
	code, out, errOut := h.run(string(b), "record", "--hook")
	if code != 0 || errOut != "" {
		h.t.Fatalf("record --hook %q: exit %d stderr %q", prompt, code, errOut)
	}
	if out == "" {
		return ""
	}
	var reply struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &reply); err != nil || reply.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		h.t.Fatalf("hook stdout is not a UserPromptSubmit answer: %v %q", err, out)
	}
	return reply.HookSpecificOutput.AdditionalContext
}

func (h *harness) minutes(args ...string) map[string]float64 {
	h.t.Helper()
	_, _, data := h.envelope(append([]string{"draft"}, args...)...)
	got := map[string]float64{}
	for _, w := range data["worklogs"].([]any) {
		w := w.(map[string]any)
		got[w["ticket"].(string)] = w["minutes"].(float64)
	}
	return got
}

func TestRetagMovesOnlyThatSessionsTimeOnThatTicket(t *testing.T) {
	h := newHarness(t)
	ops := h.worktree("ops", "feature/OPS-9-certs")
	if r := h.say(9, 0, "s1", h.repo, "fix the lock"); r != "" {
		t.Fatalf("an ordinary prompt added %q to the context", r)
	}
	h.say(9, 5, "s1", h.repo, "and the test")
	h.hook(9, 40, 0, map[string]any{"hook_event_name": "PostToolUse", "session_id": "s1", "agent_id": "a1", "cwd": ops, "duration_ms": 20 * 60 * 1000})
	h.say(11, 0, "s2", h.repo, "another conversation")
	before := h.minutes()
	if before["PUN-142"] != 15 || before["OPS-9"] == 0 {
		t.Fatalf("before the retag %v; want PUN-142 15m (10 + 5) and some OPS-9", before)
	}

	reply := h.say(9, 10, "s1", h.repo, "/sift retag pun-160")
	if !strings.Contains(reply, "moved this conversation's PUN-142 time to PUN-160") || !strings.Contains(reply, "PUN-160 now has 11m today") {
		t.Fatalf("the hook told Claude %q", reply)
	}
	after := h.minutes()
	if after["PUN-160"] != 15 || after["PUN-142"] != 5 || after["OPS-9"] != before["OPS-9"] {
		t.Fatalf("after the retag %v; want PUN-160 15m (s1, the retag prompt too), PUN-142 5m (s2), OPS-9 %vm as before", after, before["OPS-9"])
	}
	for _, e := range h.events() {
		f := e["found"].(map[string]any)
		if e["session"] == "s1" && e["agentId"] == nil && (e["ticket"] != "PUN-160" || f["via"] != "retag" || f["value"] != "PUN-142") {
			t.Fatalf("s1's prompt %v: want PUN-160 by retag of PUN-142", e)
		}
	}
	if _, out, _ := h.run("", "events"); !strings.Contains(out, "PUN-160    prompt  retag PUN-142") {
		t.Errorf("sift events:\n%s", out)
	}

	h.say(12, 0, "s1", h.repo, "one more thing")
	if got := h.minutes(); got["PUN-160"] != 20 {
		t.Fatalf("a later prompt in s1: %v, want PUN-160 20m", got)
	}
	if reply := h.say(12, 1, "s1", h.repo, "/sift:sift retag PUN-160"); !strings.Contains(reply, "already logged to PUN-160; nothing to change") {
		t.Fatalf("retagging to the same ticket again: %q", reply)
	}
	if _, _, data := h.envelope("retag", "--list"); len(data["retags"].([]any)) != 1 {
		t.Fatalf("retags %v; want the one", data["retags"])
	}
}

func TestRetagFromATime(t *testing.T) {
	h := newHarness(t)
	h.say(9, 0, "s1", h.repo, "start")
	h.say(9, 30, "s1", h.repo, "later")
	if reply := h.say(9, 35, "s1", h.repo, "/sift RETAG PUN-160 FROM 9:30"); !strings.Contains(reply, "from 09:30 on to PUN-160") {
		t.Fatalf("the hook told Claude %q", reply)
	}
	if got := h.minutes(); got["PUN-142"] != 5 || got["PUN-160"] != 10 {
		t.Fatalf("%v; want PUN-142 5m before 09:30, PUN-160 10m after", got)
	}
}

func TestARetagThatCannotBeDone(t *testing.T) {
	h := newHarness(t)
	for _, p := range []string{"/sift retag", "/sift retag soon", "/sift retag PUN-160 from 25:00"} {
		if reply := h.say(9, 0, "s1", h.repo, p); !strings.Contains(reply, "/sift retag KEY") {
			t.Errorf("%q: %q, want how to use it", p, reply)
		}
	}
	if reply := h.say(9, 1, "", h.repo, "/sift retag PUN-160"); !strings.Contains(reply, "could not tell which conversation") {
		t.Errorf("no session: %q", reply)
	}
	if _, _, data := h.envelope("retag", "--list"); len(data["retags"].([]any)) != 0 {
		t.Fatalf("retags %v; want none", data["retags"])
	}
	if got := h.minutes(); got["PUN-142"] == 0 {
		t.Fatalf("%v; the prompts are still recorded", got)
	}
}

func TestAConfirmedDayKeepsWhatItSent(t *testing.T) {
	h := newHarness(t)
	h.say(9, 0, "s1", h.repo, "start")
	h.say(9, 5, "s1", h.repo, "more")
	if code, _, _ := h.envelope("confirm", "--date", "2026-11-10"); code != 0 {
		t.Fatalf("confirm: exit %d", code)
	}
	h.now = time.Date(2026, 11, 11, 8, 0, 0, 0, time.Local)
	if code, out, _ := h.run("", "retag", "PUN-160", "--session", "s1"); code != 0 || !strings.Contains(out, "its PUN-142 time counts for PUN-160") {
		t.Fatalf("exit %d: %q", code, out)
	}
	if got := h.minutes("--date", "2026-11-10"); got["PUN-142"] != 10 || got["PUN-160"] != 0 {
		t.Fatalf("the confirmed day %v; want PUN-142 10m as sent", got)
	}
	if _, out, _ := h.run("", "events", "--date", "2026-11-10"); strings.Contains(out, "PUN-160") {
		t.Fatalf("a confirmed day's events show the later retag:\n%s", out)
	}
}

func TestRetagCommand(t *testing.T) {
	h := newHarness(t)
	h.say(9, 0, "s1", h.repo, "start")
	h.say(9, 5, "s1", h.repo, "/sift retag PUN-160")
	if code, out, _ := h.run("", "retag", "pun-170", "--session", "s1"); code != 0 || !strings.Contains(out, "its PUN-160 time counts for PUN-170") {
		t.Fatalf("a second retag moves the session's time on from where the first left it: exit %d %q", code, out)
	}
	if got := h.minutes(); got["PUN-170"] != 10 {
		t.Fatalf("%v; want PUN-170 10m", got)
	}
	h.say(9, 30, "s2", t.TempDir(), "ticketless")
	if code, _, data := h.envelope("retag", "OPS-9", "--session", "s2", "--was-none", "--from", "9:00", "--date", "2026-11-10"); code != 0 || data["saved"] != true {
		t.Fatalf("exit %d %v", code, data)
	}
	if got := h.minutes(); got["OPS-9"] != 5 {
		t.Fatalf("%v; want s2's ticketless 5m on OPS-9", got)
	}
	if _, out, _ := h.run("", "retag", "--list"); !strings.Contains(out, "s2  no ticket → OPS-9  from 2026-11-10 09:00") {
		t.Errorf("list:\n%s", out)
	}
	for _, args := range [][]string{
		{"retag"}, {"retag", "PUN-1"}, {"retag", "soon", "--session", "s1"}, {"retag", "PUN-1", "--session", "s1", "--was", "X", "--was-none"},
		{"retag", "PUN-1", "--session", "s1", "--date", "2026-11-10"}, {"retag", "PUN-1", "--session", "s1", "--from", "9"}, {"retag", "--list", "PUN-1"},
	} {
		if code, e, _ := h.envelope(args...); code != 64 || e.Outcome != "usage" {
			t.Errorf("%v: exit %d %+v", args, code, e)
		}
	}
}

func TestTheReviewPreviewsSavesAndTakesBackARetag(t *testing.T) {
	h := newHarness(t)
	h.say(9, 0, "s1", h.repo, "start")
	h.say(9, 20, "s1", h.repo, "more")
	h.say(11, 0, "s2", h.repo, "another conversation")
	other := h.worktree("ops", "feature/OPS-9-certs")
	h.say(8, 0, "s3", other, "earlier, elsewhere")
	h.now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	s := source{&run{Env: Env{Getenv: func(k string) string { return h.env[k] }, Now: func() time.Time { return h.now }}}}
	day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.Local)
	minutes := func(in tui.Input, err error) map[string]int {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int{}
		for _, w := range draft.Of(in.Day, in.Decisions, in.Round).Worklogs {
			got[w.Ticket] = w.Minutes
		}
		return got
	}
	retags := func() int {
		rs, _, _ := evidence.Retags{Dir: filepath.Join(h.env["XDG_DATA_HOME"], "sift")}.All()
		return len(rs)
	}
	before := minutes(s.Load(day))
	c := tui.RetagChange{Retag: evidence.Retag{Session: "s1", Was: "PUN-142", Ticket: "PUN-160"}}
	preview := minutes(s.Preview(day, c))
	if preview["PUN-160"] != 10 || preview["PUN-142"] != before["PUN-142"]-10 || retags() != 0 {
		t.Fatalf("before %v, preview %v, %d retags saved; want s1's 10m on PUN-160 and nothing saved", before, preview, retags())
	}
	if again := minutes(s.Load(day)); again["PUN-160"] != 0 {
		t.Fatalf("a preview leaked into the next load: %v", again)
	}
	if err := s.Retag(c); err != nil {
		t.Fatal(err)
	}
	in, err := s.Load(day)
	if saved := minutes(in, err); saved["PUN-160"] != preview["PUN-160"] || saved["PUN-142"] != preview["PUN-142"] {
		t.Fatalf("saved %v, previewed %v", saved, preview)
	}
	if len(in.Retags) != 1 || in.Retags[0].Session != "s1" || !in.Retags[0].At.Equal(h.now) {
		t.Fatalf("the day's retags %+v", in.Retags)
	}
	if err := s.Retag(tui.RetagChange{Retag: evidence.Retag{Session: "s1", Was: "PUN-160", Ticket: "PUN-160"}}); err == nil {
		t.Fatal("a retag onto the ticket it moves from saved")
	}
	undo := tui.RetagChange{Retag: in.Retags[0], Undo: true}
	if back := minutes(s.Preview(day, undo)); back["PUN-142"] != before["PUN-142"] || back["PUN-160"] != 0 || retags() != 1 {
		t.Fatalf("undo previews %v, %d retags; want %v and the retag still saved", back, retags(), before)
	}
	if err := s.Retag(undo); err != nil {
		t.Fatal(err)
	}
	if after := minutes(s.Load(day)); after["PUN-142"] != before["PUN-142"] || retags() != 0 {
		t.Fatalf("after undo %v with %d retags, want %v", after, retags(), before)
	}
	s.Retag(c)
	if got := strings.Join(s.Recent(day), ","); got != "PUN-160,PUN-142,OPS-9" {
		t.Fatalf("recent %q; want the retag's ticket, then the prompts' newest first", got)
	}
}
