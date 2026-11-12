package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTheGateMatchesOnlyAConfirmingCall(t *testing.T) {
	ask := []string{
		"sift confirm --yes",
		"sift confirm --date 2026-11-10 --yes",
		"cd x && sift confirm --yes --json",
		"/usr/local/bin/sift confirm --yes",
		"echo hi; sift confirm --yes",
	}
	pass := []string{
		"sift confirm",
		"sift confirm --date 2026-11-10",
		"sift draft --yes",
		"sift confirm; echo --yes",
		"grep 'swift confirm --yes' notes",
		"git commit -m 'sift confirm'",
	}
	for _, c := range ask {
		if !GatesConfirm(c) {
			t.Errorf("should ask: %q", c)
		}
	}
	for _, c := range pass {
		if GatesConfirm(c) {
			t.Errorf("should pass: %q", c)
		}
	}
}

func TestTheGateHookAsksAndNeverAllows(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run(`{"tool_name":"Bash","tool_input":{"command":"sift confirm --yes"}}`, "gate", "--hook")
	if code != 0 || !strings.Contains(out, `"permissionDecision":"ask"`) {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, in := range []string{`{"tool_name":"Bash","tool_input":{"command":"ls"}}`, `{"tool_name":"Read","tool_input":{}}`} {
		if code, out, _ := h.run(in, "gate", "--hook"); code != 0 || out != "" {
			t.Errorf("%s: exit %d, %q", in, code, out)
		}
	}
	if _, out, _ := h.run("not json", "gate", "--hook"); !strings.Contains(out, `"ask"`) {
		t.Errorf("unreadable input must ask, got %q", out)
	}
}

func TestTheNudgeNamesTheDaysWaiting(t *testing.T) {
	h := newHarness(t)
	h.prompt(9, 0, h.repo) // Tue 10 Nov
	h.now = time.Date(2026, 11, 12, 9, 0, 0, 0, time.Local)
	code, out, _ := h.run("", "status", "--hook")
	var msg map[string]string
	if code != 0 || json.Unmarshal([]byte(out), &msg) != nil || !strings.Contains(msg["systemMessage"], "1 day waiting for review, from 2026-11-10") {
		t.Fatalf("exit %d: %q", code, out)
	}
	os.MkdirAll(filepath.Join(h.env["XDG_DATA_HOME"], "sift", "days"), 0o700)
	os.WriteFile(filepath.Join(h.env["XDG_DATA_HOME"], "sift", "days", "2026-11-10.json"), []byte(`{"v":1,"date":"2026-11-10","confirmed":{"at":"2026-11-10T18:00:00Z","worklogs":[]}}`), 0o600)
	if _, out, _ := h.run("", "status", "--hook"); out != "" {
		t.Fatalf("a confirmed day is not waiting: %q", out)
	}
}
