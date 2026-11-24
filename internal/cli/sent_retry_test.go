package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A confirm whose outbox write lands but whose decisions write does not
// (here: the days lock is busy; a crash at that point is the same) leaves
// part 0 in the outbox with no watermark. Once that part is synced, a later
// confirm reuses part 0: the synced worklog is kept as it was, the decisions
// record the larger tally, and the work in between is never sent.
func TestAConfirmThatCouldNotRecordSendsNothing(t *testing.T) {
	h := newHarness(t)
	f := h.jira(t)
	for m := 0; m < 30; m += 3 {
		h.prompt(9, m, h.repo)
	}
	dir := filepath.Join(h.env["XDG_DATA_HOME"], "sift")
	os.MkdirAll(dir, 0o700)
	lf, err := os.OpenFile(filepath.Join(dir, "days.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(lf.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	h.now = time.Date(2026, 11, 10, 10, 0, 0, 0, time.Local)
	code, out, errOut := h.run("", "confirm")
	t.Logf("confirm with days.lock held: exit %d\n%s%s", code, out, errOut)
	lf.Close()
	code, out, _ = h.run("", "sync")
	t.Logf("sync: exit %d\n%s", code, out)

	for m := 0; m < 30; m += 3 {
		h.prompt(11, m, h.repo)
	}
	h.now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	code, out, _ = h.run("", "confirm")
	t.Logf("confirm at 12:00: exit %d\n%s", code, out)
	h.now = time.Date(2026, 11, 10, 13, 0, 0, 0, time.Local)
	code, out, _ = h.run("", "confirm")
	t.Logf("confirm at 13:00: exit %d\n%s", code, out)
	code, out, _ = h.run("", "draft")
	t.Logf("draft at 13:00:\n%s", out)
	for _, p := range f.posts {
		t.Logf("post %s", p)
	}
	_, _, data := h.envelope("draft")
	if got := postedMinutes(t, f); got["PUN-142"] != 64 || data["sentTotal"].(float64) != 64 {
		t.Fatalf("Jira holds %v and sift says %vm sent; want PUN-142's 64m (9:00-9:32 and 11:00-11:32) in both", got, data["sentTotal"])
	}
}
