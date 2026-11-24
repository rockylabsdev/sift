package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A day the previous sift confirmed keeps its adjust map (the old confirm
// never cleared it, and the minutes it added are inside the confirmed
// worklogs). The new draft applies that adjust again to the empty time
// after the end-of-day watermark, so the day waits for review and a
// confirm sends the adjusted minutes a second time.
func TestAnOldConfirmedDayWithAnAdjustSendsNothingMore(t *testing.T) {
	h := newHarness(t)
	f := h.jira(t)
	h.prompt(9, 0, h.repo)
	dir := filepath.Join(h.env["XDG_DATA_HOME"], "sift")
	os.MkdirAll(filepath.Join(dir, "days"), 0o700)
	// as the previous sift wrote them: PUN-142 5m counted + 25m by hand = 30m, confirmed and synced
	os.WriteFile(filepath.Join(dir, "days", "2026-11-10.json"), []byte(`{
  "v": 1,
  "date": "2026-11-10",
  "adjust": {"PUN-142": 25},
  "confirmed": {"at": "2026-11-10T18:05:00Z", "worklogs": [{"ticket": "PUN-142", "minutes": 30, "counted": 5, "assigned": 0, "byHand": 25, "agent": 0, "started": "2026-11-10T09:00:00Z"}]}
}
`), 0o600)
	os.WriteFile(filepath.Join(dir, "worklogs.json"), []byte(`{"v":1,"worklogs":[{"date":"2026-11-10","ticket":"PUN-142","started":"2026-11-10T09:00:00Z","minutes":30,"state":"synced","remoteId":"77","syncedAt":"2026-11-10T18:05:01Z"}]}
`), 0o600)

	h.now = time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	_, out, _ := h.run("", "draft", "--date", "2026-11-10")
	t.Logf("draft:\n%s", out)
	_, _, data := h.envelope("status")
	t.Logf("status waiting: %v", data["waiting"])
	code, out, _ := h.run("", "confirm", "--date", "2026-11-10")
	t.Logf("confirm: exit %d\n%s", code, out)
	for _, p := range f.posts {
		t.Logf("post %s", p)
	}
	if len(f.posts) != 0 {
		t.Fatalf("a day the previous sift confirmed and sent posted %d more worklogs; want none", len(f.posts))
	}
}
