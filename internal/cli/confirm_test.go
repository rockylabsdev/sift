package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeJira records worklog posts and answers with an id, or with status.
type fakeJira struct {
	mu     sync.Mutex
	posts  []string
	status int
}

func (f *fakeJira) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status != 0 {
		w.WriteHeader(f.status)
		io.WriteString(w, `{"errorMessages":["down"]}`)
		return
	}
	body, _ := io.ReadAll(r.Body)
	f.posts = append(f.posts, r.URL.Path+" "+string(body))
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"id":"`+string(rune('0'+len(f.posts)))+`"}`)
}

func (h *harness) jira(t *testing.T) *fakeJira {
	f := &fakeJira{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	os.MkdirAll(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift"), 0o700)
	os.WriteFile(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift", "config.json"),
		[]byte(`{"tracker":"jira","jira":{"site":"`+srv.URL+`","email":"me@acme.test"}}`), 0o600)
	h.env["SIFT_JIRA_TOKEN"] = "t0ken"
	return f
}

func TestConfirmSendsOneWorklogPerTicketOnce(t *testing.T) {
	h := newHarness(t)
	f := h.jira(t)
	h.prompt(9, 0, h.repo)
	h.prompt(9, 5, h.repo)
	code, e, data := h.envelope("confirm", "--date", "2026-11-10")
	if code != 0 || !e.OK || data["sent"].(float64) != 1 {
		t.Fatalf("exit %d %+v %v", code, e, data)
	}
	if len(f.posts) != 1 || !strings.Contains(f.posts[0], "/issue/PUN-142/worklog") || !strings.Contains(f.posts[0], `"timeSpentSeconds":600`) {
		t.Fatalf("posts %v", f.posts)
	}
	code, _, data = h.envelope("confirm", "--date", "2026-11-10")
	if code != 0 || data["already"] != true || len(f.posts) != 1 {
		t.Fatalf("confirming again: exit %d, %v, %d posts", code, data, len(f.posts))
	}
	if code, out, _ := h.run("", "block", "OPS-77", "10:00-11:00", "--date", "2026-11-10"); code == 0 {
		t.Fatalf("a confirmed day took a block: %q", out)
	}
	_, _, data = h.envelope("log", "--from", "2026-11-09", "--to", "2026-11-11")
	if rows := data["rows"].([]any); len(rows) != 1 || rows[0].(map[string]any)["state"] != "synced" {
		t.Fatalf("log %v", data)
	}
}

func TestAFailedSendIsKeptForSync(t *testing.T) {
	h := newHarness(t)
	f := h.jira(t)
	f.status = 503
	h.prompt(9, 0, h.repo)
	code, e, _ := h.envelope("confirm", "--date", "2026-11-10")
	if code != 7 || !e.OK || e.Outcome != "sync_pending" {
		t.Fatalf("exit %d %+v; want 7, ok: the day is confirmed and only the send is owed", code, e)
	}
	f.status = 0
	code, _, data := h.envelope("sync")
	if code != 0 || len(data["sent"].([]any)) != 1 || len(f.posts) != 1 {
		t.Fatalf("sync: exit %d %v posts %v", code, data, f.posts)
	}
	if code, _, data := h.envelope("sync"); code != 0 || len(data["sent"].([]any)) != 0 {
		t.Fatalf("a second sync sent again: %v", data)
	}
}

func TestAnAgentsConfirmIsAProposal(t *testing.T) {
	h := newHarness(t)
	f := h.jira(t)
	h.prompt(9, 0, h.repo)
	h.env["CLAUDECODE"] = "1"
	code, e, data := h.envelope("confirm", "--date", "2026-11-10")
	if code != 6 || e.Outcome != "needs_confirmation" || data["preview"] == nil || len(f.posts) != 0 {
		t.Fatalf("exit %d %+v posts %v", code, e, f.posts)
	}
	if code, _, _ := h.envelope("confirm", "--date", "2026-11-10", "--yes"); code != 0 || len(f.posts) != 1 {
		t.Fatalf("with --yes (which the hook puts to the user): exit %d posts %v", code, f.posts)
	}
}

func TestWithoutATrackerConfirmLogsLocally(t *testing.T) {
	h := newHarness(t)
	h.prompt(9, 0, h.repo)
	code, _, data := h.envelope("confirm", "--date", "2026-11-10")
	ws := data["worklogs"].([]any)
	if code != 0 || len(ws) != 1 || ws[0].(map[string]any)["state"] != "local" {
		t.Fatalf("exit %d %v", code, data)
	}
}

func TestJiraNotSetUp(t *testing.T) {
	h := newHarness(t)
	h.jira(t)
	delete(h.env, "SIFT_JIRA_TOKEN")
	h.prompt(9, 0, h.repo)
	if code, e, _ := h.envelope("confirm", "--date", "2026-11-10"); code != 3 || e.Outcome != "not_configured" {
		t.Fatalf("exit %d %+v", code, e)
	}
}

func TestImportPunchcard(t *testing.T) {
	h := newHarness(t)
	bin := filepath.Join(t.TempDir(), "punchcard")
	os.WriteFile(bin, []byte(`#!/bin/sh
echo '{"v":1,"ok":true,"outcome":"ok","command":"log","data":{"rows":[{"date":"2026-11-03","started":"2026-11-03T09:00:00Z","key":"PUN-99","minutes":90,"state":"synced","remoteId":"5"},{"date":"2026-11-04","started":"2026-11-04T09:00:00Z","key":"PUN-99","minutes":30,"state":"pending"}]},"events":[],"error":null}'
`), 0o755)
	h.env["SIFT_PUNCHCARD"] = bin
	code, e, data := h.envelope("import", "punchcard")
	if code != 0 || data["imported"].(float64) != 2 || len(e.Events) != 1 {
		t.Fatalf("exit %d %+v %v", code, e, data)
	}
	_, _, data = h.envelope("log", "--from", "2026-11-01", "--to", "2026-11-10")
	rows := data["rows"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["from"] != "punchcard" {
		b, _ := json.Marshal(rows)
		t.Fatalf("log %s", b)
	}
}
