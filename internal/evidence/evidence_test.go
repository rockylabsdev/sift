package evidence

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func key(s string) *string { return &s }

func TestAppendThenRead(t *testing.T) {
	l := Log{Dir: t.TempDir()}
	at := time.Date(2026, 11, 30, 23, 59, 30, 0, time.UTC)
	e := Event{At: at, Source: "claude-code", Kind: "prompt", Ticket: key("PUN-142"), Found: &Found{"branch", "feature/PUN-142-x"}, Dir: "/w", Origin: Prompt}
	if err := l.Append(e); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(Event{At: at.Add(time.Minute), Source: "claude-code", Kind: "prompt", Dir: "/w", Origin: Hand}); err != nil {
		t.Fatal(err)
	}
	got, problems, err := l.Between(at.Add(-time.Hour), at.Add(time.Hour))
	if err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	if len(got) != 2 || *got[0].Ticket != "PUN-142" || got[0].V != Version || got[1].Ticket != nil {
		t.Fatalf("read back %+v", got)
	}
	if _, err := os.Stat(filepath.Join(l.Dir, "events", "2026-12.jsonl")); err != nil {
		t.Errorf("an event after midnight UTC on the 30th goes in December's file: %v", err)
	}
}

func TestAgentActivityIsNotEvidence(t *testing.T) {
	l := Log{Dir: t.TempDir()}
	err := l.Append(Event{At: time.Now(), Origin: Agent})
	if !errors.Is(err, ErrNotEvidence) {
		t.Fatalf("err = %v, want ErrNotEvidence", err)
	}
	if _, err := os.Stat(filepath.Join(l.Dir, "events")); !os.IsNotExist(err) {
		t.Errorf("a refused event still created the log")
	}
}

func TestConcurrentAppendsKeepEveryLine(t *testing.T) {
	l := Log{Dir: t.TempDir()}
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if err := l.Append(Event{At: at, Kind: "prompt", Origin: Prompt}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	got, problems, err := l.Between(at, at.Add(time.Second))
	if err != nil || len(problems) > 0 || len(got) != 800 {
		t.Fatalf("read %d events, %v, %v; want 800", len(got), problems, err)
	}
}

func TestUnreadableLinesAreReportedNotFatal(t *testing.T) {
	l := Log{Dir: t.TempDir()}
	path := filepath.Join(l.Dir, "events", "2026-11.jsonl")
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte(`{"v":1,"at":"2026-11-10T09:00:00Z","origin":"prompt"}
not json
{"v":9,"at":"2026-11-10T09:01:00Z","origin":"prompt"}
{"v":1,"at":"2026-11-10T09:02:00Z","origin":"agent"}
`), 0o600)
	got, problems, err := l.Between(time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC))
	if err != nil || len(got) != 1 || len(problems) != 2 {
		t.Fatalf("got %d events, problems %+v, err %v", len(got), problems, err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestTicketFromBranchThenFile(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "feature/pun-142-side-lock")
	sub := filepath.Join(repo, "a", "b")
	os.MkdirAll(sub, 0o700)

	k, f := TicketFor(sub, nil)
	if k == nil || *k != "PUN-142" || f.Via != "branch" || f.Value != "feature/pun-142-side-lock" {
		t.Fatalf("from the branch: %v %+v", k, f)
	}

	os.WriteFile(filepath.Join(repo, "a", ".punchcard"), []byte("# the ticket\nops-77\n"), 0o600)
	k, f = TicketFor(sub, nil)
	if k == nil || *k != "OPS-77" || f.Via != "file" {
		t.Fatalf("from .punchcard: %v %+v", k, f)
	}

	os.WriteFile(filepath.Join(sub, ".sift"), []byte("PUN-150\n"), 0o600)
	if k, _ = TicketFor(sub, nil); *k != "PUN-150" {
		t.Fatalf("the nearest file wins: %v", *k)
	}
}

func TestNoTicketOutsideARepo(t *testing.T) {
	if k, f := TicketFor(t.TempDir(), nil); k != nil || f != nil {
		t.Fatalf("got %v %+v", *k, f)
	}
}

func TestProjectsFilterTheBranchRule(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "fix-2/pun-142-side-lock")
	if k, _ := TicketFor(repo, nil); k == nil || *k != "FIX-2" {
		t.Fatalf("without projects the first key-like text wins: %v", k)
	}
	if k, _ := TicketFor(repo, []string{"PUN", "OPS"}); k == nil || *k != "PUN-142" {
		t.Fatalf("with projects: %v", k)
	}
	git(t, repo, "checkout", "-q", "-b", "release-2026")
	if k, f := TicketFor(repo, []string{"PUN"}); k != nil || f != nil {
		t.Fatalf("a branch with no listed project has no ticket: %v", *k)
	}
}
