package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func mkOps(h *harness) string {
	ops := filepath.Join(h.repo, "ops")
	os.MkdirAll(ops, 0o700)
	os.WriteFile(filepath.Join(ops, ".sift"), []byte("OPS-77\n"), 0o600)
	return ops
}

// A second confirm that loaded the day before the first one recorded its
// send writes the same part again; the minutes only it saw are sent in that
// part, are not recorded, and a later confirm sends them again.
func TestAConfirmThatReadTheDayEarlySendsOnlyWhatIsNew(t *testing.T) {
	h := newHarness(t)
	f := h.jira(t)
	ops := mkOps(h)
	for m := 0; m < 30; m += 3 {
		h.prompt(9, m, h.repo)
	}
	h.prompt(10, 2, ops) // seen by B (now 10:05), not by A (now 10:00)

	var held sync.Once
	loaded := make(chan struct{})
	release := make(chan struct{})
	done := make(chan int)
	go func() {
		var o, e bytes.Buffer
		env := Env{
			Getenv: func(k string) string { return h.env[k] },
			Stdin:  strings.NewReader(""), Stdout: &o, Stderr: &e,
			Now: func() time.Time {
				pc, _, _, _ := runtime.Caller(1)
				if fn := runtime.FuncForPC(pc); fn != nil && strings.HasSuffix(fn.Name(), "confirmDay") {
					held.Do(func() {
						close(loaded) // B has loaded the day: nothing sent yet
						<-release
					})
				}
				return time.Date(2026, 11, 10, 10, 5, 0, 0, time.Local)
			},
		}
		done <- Run([]string{"confirm", "--date", "2026-11-10", "--json"}, env)
	}()
	<-loaded
	h.now = time.Date(2026, 11, 10, 10, 0, 0, 0, time.Local)
	if code, _, _ := h.envelope("confirm", "--date", "2026-11-10"); code != 0 {
		t.Fatalf("A: exit %d", code)
	}
	close(release)
	if code := <-done; code != 0 {
		t.Fatalf("B: exit %d", code)
	}
	h.now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	h.envelope("confirm", "--date", "2026-11-10")
	for _, p := range f.posts {
		t.Logf("post %s", p)
	}
	// OPS-77's one prompt at 10:02 and its tail: 10:02-10:07, sent once in all
	if got := postedMinutes(t, f); got["OPS-77"] != 5 || got["PUN-142"] != 32 {
		t.Fatalf("Jira holds %v; want OPS-77 5m and PUN-142 32m, each minute once", got)
	}
}
