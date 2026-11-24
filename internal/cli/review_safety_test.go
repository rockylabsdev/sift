package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rockylabsdev/sift/internal/draft"
)

// postedMinutes sums timeSpentSeconds/60 per ticket over the fake Jira's posts.
func postedMinutes(t *testing.T, f *fakeJira) map[string]int {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int{}
	for _, p := range f.posts {
		path, body, _ := strings.Cut(p, " ")
		var b struct {
			TimeSpentSeconds int `json:"timeSpentSeconds"`
		}
		json.Unmarshal([]byte(body), &b)
		parts := strings.Split(path, "/")
		out[parts[len(parts)-2]] += b.TimeSpentSeconds / 60
	}
	return out
}

func draftMinutes(t *testing.T, h *harness, date string) map[string]int {
	t.Helper()
	_, _, data := h.envelope("draft", "--date", date)
	out := map[string]int{}
	for _, w := range data["worklogs"].([]any) {
		w := w.(map[string]any)
		out[w["ticket"].(string)] += int(w["minutes"].(float64))
	}
	return out
}

// Two confirms of the same day running at once, at different clock times
// (B loaded its draft at 10:56 while A, at 10:00, was still confirming),
// send the minutes between the two watermarks twice. B is held, through its
// clock, right after it read the day and before it writes the outbox:
// exactly where a second process can be while the first runs.
func TestConcurrentConfirmsSendEachTicketOnce(t *testing.T) {
	h := newHarness(t)
	f := h.jira(t)
	ops := filepath.Join(h.repo, "ops")
	os.MkdirAll(ops, 0o700)
	os.WriteFile(filepath.Join(ops, ".sift"), []byte("OPS-77\n"), 0o600)
	for m := 0; m < 60; m += 3 {
		h.prompt(9, m, h.repo)
	}
	for m := 0; m < 30; m += 3 {
		h.prompt(10, m, ops)
	}
	doc := filepath.Join(h.repo, "doc")
	os.MkdirAll(doc, 0o700)
	os.WriteFile(filepath.Join(doc, ".sift"), []byte("DOC-1\n"), 0o600)
	for m := 40; m < 55; m += 3 {
		h.prompt(10, m, doc) // only B, at 10:56, has seen DOC-1
	}
	h.now = time.Date(2026, 11, 10, 23, 0, 0, 0, time.Local)
	want := draftMinutes(t, h, "2026-11-10")

	env := func(now time.Time, hold chan struct{}) Env {
		return Env{Getenv: func(k string) string { return h.env[k] },
			Stdin: strings.NewReader(""), Stdout: &strings.Builder{}, Stderr: &strings.Builder{},
			Now: func() time.Time {
				if hold != nil {
					pc, _, _, _ := runtime.Caller(1)
					if strings.HasSuffix(runtime.FuncForPC(pc).Name(), "confirmDay") {
						<-hold
					}
				}
				return now
			}}
	}
	aDone := make(chan struct{})
	bDone := make(chan struct{})
	go func() {
		Run([]string{"confirm", "--json"}, env(time.Date(2026, 11, 10, 10, 56, 0, 0, time.Local), aDone))
		close(bDone)
	}()
	time.Sleep(200 * time.Millisecond) // B has read the day and waits on its clock
	Run([]string{"confirm", "--json"}, env(time.Date(2026, 11, 10, 10, 0, 0, 0, time.Local), nil))
	close(aDone)
	<-bDone
	h.envelope("confirm", "--date", "2026-11-10")
	h.envelope("sync")
	got := postedMinutes(t, f)
	t.Logf("posts %v", f.posts)
	b, _ := os.ReadFile(filepath.Join(h.env["XDG_DATA_HOME"], "sift", "days", "2026-11-10.json"))
	t.Logf("decisions %s", b)
	bad := false
	for k, v := range want {
		if got[k] != v {
			bad = true
			t.Logf("Jira holds %s %dm; the day has %dm", k, got[k], v)
		}
	}
	if bad {
		t.Fatalf("Jira %v, day %v", got, want)
	}
}

func calendarAt(t *testing.T, h *harness, ics string) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(ics)) }))
	t.Cleanup(srv.Close)
	secrets := filepath.Join(h.env["XDG_CONFIG_HOME"], "sift", "secrets")
	os.MkdirAll(secrets, 0o700)
	os.WriteFile(filepath.Join(secrets, "calendar-url"), []byte(srv.URL), 0o600)
}

const meeting11 = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:sync\r\nSUMMARY:1:1 with Sam\r\nDTSTART:20261110T110000\r\nDTEND:20261110T113000\r\nORGANIZER:mailto:sam@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

// The hook's "waiting" check derives without the calendar, so a partly
// sent day whose only work after the watermark sat inside a meeting nags
// for review forever while confirm has nothing to send.
func TestADayWithNothingToSendStopsWaiting(t *testing.T) {
	h := newHarness(t)
	h.jira(t)
	calendarAt(t, h, meeting11)
	os.WriteFile(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift", "config.json"),
		[]byte(`{"email":"me@example.com","tracker":"jira","jira":{"site":"`+readSite(t, h)+`","email":"me@acme.test"}}`), 0o600)
	h.prompt(9, 0, h.repo)
	h.now = time.Date(2026, 11, 10, 10, 0, 0, 0, time.Local)
	if code, _, _ := h.envelope("confirm"); code != 0 {
		t.Fatalf("confirm exit %d", code)
	}
	for m := 0; m < 22; m += 3 {
		h.prompt(11, m, h.repo) // inside the 1:1, which wins over the prompts
	}
	h.now = time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	_, out, _ := h.run("", "status", "--hook")
	_, _, c := h.envelope("confirm", "--date", "2026-11-10")
	_, out2, _ := h.run("", "status", "--hook")
	t.Logf("hook before confirm: %q", out)
	t.Logf("confirm: already=%v minutes=%v", c["already"], c["minutes"])
	t.Logf("hook after confirm: %q", out2)
	if c["already"] == true && strings.Contains(out2, "waiting for review") {
		t.Fatalf("the day still waits for review, yet confirm has nothing to send")
	}
}

// The reverse: meeting time after the watermark given to a ticket is ready
// to send, but the hook (no calendar) does not say the day waits.
func TestTheHookSeesAssignedMeetingTimeReadyToSend(t *testing.T) {
	h := newHarness(t)
	h.jira(t)
	calendarAt(t, h, meeting11)
	os.WriteFile(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift", "config.json"),
		[]byte(`{"email":"me@example.com","tracker":"jira","jira":{"site":"`+readSite(t, h)+`","email":"me@acme.test"}}`), 0o600)
	h.prompt(9, 0, h.repo)
	h.now = time.Date(2026, 11, 10, 10, 0, 0, 0, time.Local)
	if code, _, _ := h.envelope("confirm"); code != 0 {
		t.Fatalf("confirm exit %d", code)
	}
	// in review, the user gives the 1:1 to PUN-142 (what `t` on the row saves)
	h.now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	if _, err := (draft.Store{Dir: filepath.Join(h.env["XDG_DATA_HOME"], "sift")}).Update("2026-11-10", func(d *draft.Decisions) error {
		d.Assign = map[string]string{"meeting@11:00": "PUN-142"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := draftMinutes(t, h, "2026-11-10"); got["PUN-142"] != 30 {
		t.Fatalf("draft %v; want the 1:1's 30m ready for PUN-142", got)
	}
	h.now = time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	_, hook, _ := h.run("", "status", "--hook")
	_, _, st := h.envelope("status")
	t.Logf("status --hook: %q", hook)
	t.Logf("status waiting: %v", st["waiting"])
	_, _, c := h.envelope("confirm", "--date", "2026-11-10")
	t.Logf("confirm --date 2026-11-10: minutes=%v", c["minutes"])
	if !strings.Contains(hook, "waiting for review") {
		t.Fatalf("the hook says nothing waits, yet confirm sends %vm", c["minutes"])
	}
}

func readSite(t *testing.T, h *harness) string {
	b, err := os.ReadFile(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct{ Jira struct{ Site string } }
	json.Unmarshal(b, &c)
	return c.Jira.Site
}

// A confirm that recorded its send and died before writing the outbox is
// finished by the next confirm, even one with nothing new to send.
func TestAConfirmThatDiedAfterRecordingIsFinishedByTheNext(t *testing.T) {
	h := newHarness(t)
	f := h.jira(t)
	for m := 0; m < 30; m += 3 {
		h.prompt(9, m, h.repo)
	}
	h.now = time.Date(2026, 11, 10, 10, 0, 0, 0, time.Local)
	r := &run{Env: Env{Getenv: func(k string) string { return h.env[k] }, Now: func() time.Time { return h.now }}}
	l, err := r.load("2026-11-10")
	if err != nil {
		t.Fatal(err)
	}
	dr := draft.Of(l.Day, l.Decisions, l.Config.Round)
	if _, err := l.Store.Update("2026-11-10", func(d *draft.Decisions) error {
		d.Sent = append(d.Sent, draft.Send{At: h.now.UTC(), Through: h.now.Add(time.Minute), Worklogs: dr.Tally(), Runs: l.Day.Runs, Stretches: l.Day.Stretches})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := h.envelope("sync"); code != 0 || len(f.posts) != 0 {
		t.Fatalf("sync: exit %d, posts %v; the outbox is empty", code, f.posts)
	}
	if code, _, data := h.envelope("confirm"); code != 0 || data["already"] != true || len(f.posts) != 1 {
		t.Fatalf("confirm with nothing new: exit %d, %v, posts %v; want the recorded part sent", code, data, f.posts)
	}
	if code, _, _ := h.envelope("confirm"); code != 0 || len(f.posts) != 1 {
		t.Fatalf("a third confirm sent again: %v", f.posts)
	}
	if got := postedMinutes(t, f); got["PUN-142"] != 32 {
		t.Fatalf("Jira holds %v; want PUN-142 32m", got)
	}
}
