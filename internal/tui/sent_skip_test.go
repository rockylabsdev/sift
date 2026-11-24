package tui

import (
	"testing"
)

// A meeting skipped before a send, and wholly before its watermark, still
// gets its own row after the send, and x on it counts it again: a change to
// sent time that is not refused.
func TestASkippedMeetingInSentTimeCannotBeCountedAgain(t *testing.T) {
	f := newFake()
	m := sized(New(f, day0, ""), 160, 38)
	for i := 0; i < 20; i++ {
		if r, _ := m.selectedRow(); r.MeetingID == "standup@10:00" {
			break
		}
		m = press(m, "down")
	}
	m = press(m, "x")
	if len(f.dec["2026-11-10"].Skip) != 1 {
		t.Fatalf("x did not skip the standup")
	}
	f.through = hm(10, 45)
	m = press(m, "c", "y")
	beforeShown := frame(m)
	for i := 0; i < 20; i++ {
		if r, _ := m.selectedRow(); r.skipped {
			break
		}
		m = press(m, "down")
	}
	r, _ := m.selectedRow()
	t.Logf("selected row: skipped=%v sent=%v %s-%s", r.skipped, r.sent, r.From.Format("15:04"), r.To.Format("15:04"))
	m = press(m, "x")
	t.Logf("msg after x: %q; skip now %v", m.msg, f.dec["2026-11-10"].Skip)
	if len(f.dec["2026-11-10"].Skip) != 1 {
		t.Fatalf("x on the 10:00-10:30 standup, sent through 10:45, changed sent time: skip %v, msg %q\nbefore:\n%s\nafter:\n%s",
			f.dec["2026-11-10"].Skip, m.msg, beforeShown, frame(m))
	}
}
