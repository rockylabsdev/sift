package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func (h *harness) hook(hh, mm, ss int, in map[string]any) {
	h.t.Helper()
	defer func(now time.Time) { h.now = now }(h.now)
	h.now = time.Date(2026, 11, 10, hh, mm, ss, 0, time.Local)
	b, _ := json.Marshal(in)
	if code, out, errOut := h.run(string(b), "record", "--hook"); code != 0 || out != "" || errOut != "" {
		h.t.Fatalf("record --hook %v: exit %d stdout %q stderr %q", in, code, out, errOut)
	}
}

// worktree adds a git worktree of h.repo on branch, as Claude Code's
// Agent isolation and Orca do, and returns its directory.
func (h *harness) worktree(name, branch string) string {
	h.t.Helper()
	exec.Command("git", "-C", h.repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init").Run()
	dir := filepath.Join(h.repo, ".claude", "worktrees", name)
	if out, err := exec.Command("git", "-C", h.repo, "worktree", "add", "-q", "-b", branch, dir).CombinedOutput(); err != nil {
		h.t.Fatalf("worktree: %v %s", err, out)
	}
	return dir
}

func (h *harness) events() []map[string]any {
	h.t.Helper()
	_, _, data := h.envelope("events")
	var out []map[string]any
	for _, e := range data["events"].([]any) {
		out = append(out, e.(map[string]any))
	}
	return out
}

func TestASubagentInAClaudeCodeWorktreeCountsForTheSessionsTicket(t *testing.T) {
	h := newHarness(t)
	wt := h.worktree("agent-3f9a1c", "worktree-agent-3f9a1c")
	h.hook(9, 0, 0, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": "s1", "cwd": h.repo})
	h.hook(9, 1, 0, map[string]any{"hook_event_name": "SubagentStart", "session_id": "s1", "agent_id": "3f9a1c", "cwd": wt})
	h.hook(9, 30, 0, map[string]any{"hook_event_name": "PostToolUse", "session_id": "s1", "agent_id": "3f9a1c", "cwd": wt, "duration_ms": 25 * 60 * 1000})

	evs := h.events()
	if len(evs) != 3 {
		t.Fatalf("%d events: %v", len(evs), evs)
	}
	for _, e := range evs[1:] {
		f, _ := e["found"].(map[string]any)
		if e["ticket"] != "PUN-142" || f["via"] != "session" || e["origin"] != "agent" || e["agentId"] != "3f9a1c" {
			t.Fatalf("the subagent's event %v: want PUN-142 through the session", e)
		}
	}
	_, _, data := h.envelope("draft")
	w := data["worklogs"].([]any)[0].(map[string]any)
	if w["ticket"] != "PUN-142" || w["minutes"].(float64) != 35 || w["agent"].(float64) != 30 {
		t.Fatalf("worklog %v: want 35m, 30 of them agent (09:01 start, the 25m tool run to 09:30, its tail)", w)
	}
}

func TestAWorktreeThatNamesATicketKeepsIt(t *testing.T) {
	h := newHarness(t)
	wt := h.worktree("ops", "feature/OPS-77-certs")
	h.hook(9, 0, 0, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": "s1", "cwd": h.repo})
	h.hook(9, 2, 0, map[string]any{"hook_event_name": "PostToolUse", "session_id": "s1", "agent_id": "a1", "cwd": wt})
	if e := h.events()[1]; e["ticket"] != "OPS-77" || e["found"] == nil || e["found"].(map[string]any)["via"] != "branch" {
		t.Fatalf("%v: the worktree's own branch names OPS-77", e)
	}
	os.WriteFile(filepath.Join(wt, ".sift"), []byte("PUN-150\n"), 0o600)
	h.hook(9, 4, 0, map[string]any{"hook_event_name": "PostToolUse", "session_id": "s1", "agent_id": "a1", "cwd": wt})
	if e := h.events()[2]; e["ticket"] != "PUN-150" {
		t.Fatalf("%v: a .sift file wins", e)
	}
}

func TestAgentEventsArePaced(t *testing.T) {
	h := newHarness(t)
	h.hook(9, 0, 0, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": "s1", "cwd": h.repo})
	for s := 0; s < 50; s += 5 {
		h.hook(9, 1, s, map[string]any{"hook_event_name": "PostToolUse", "session_id": "s1", "cwd": h.repo, "duration_ms": 2000})
	}
	h.hook(9, 1, 55, map[string]any{"hook_event_name": "PostToolUse", "session_id": "s1", "cwd": h.repo, "duration_ms": 90_000})
	if n := len(h.events()); n != 3 {
		t.Fatalf("%d events; want the prompt, one paced tool event, and the long run", n)
	}
}

func TestAgentTimeOff(t *testing.T) {
	h := newHarness(t)
	os.MkdirAll(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift"), 0o700)
	os.WriteFile(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift", "config.json"), []byte(`{"agent_time":"off"}`), 0o600)
	h.hook(9, 0, 0, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": "s1", "cwd": h.repo})
	h.hook(9, 40, 0, map[string]any{"hook_event_name": "PostToolUse", "session_id": "s1", "cwd": h.repo, "duration_ms": 30 * 60 * 1000})
	_, _, data := h.envelope("draft")
	if w := data["worklogs"].([]any)[0].(map[string]any); w["minutes"].(float64) != 5 {
		t.Fatalf("with agent_time off only the prompt counts: %v", w)
	}
	os.WriteFile(filepath.Join(h.env["XDG_CONFIG_HOME"], "sift", "config.json"), []byte(`{"agent_time":"sometimes"}`), 0o600)
	if code, _, _ := h.envelope("draft"); code != 1 {
		t.Fatalf("a bad agent_time: exit %d", code)
	}
}
