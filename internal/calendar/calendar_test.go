package calendar

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const owner = "me@example.com"

var berlin = mustZone("Europe/Berlin")

func mustZone(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

func at(day, hhmm string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", day+" "+hhmm, berlin)
	if err != nil {
		panic(err)
	}
	return t
}

func parse(t *testing.T, fixture, day string) ([]Meeting, []Skipped) {
	t.Helper()
	ics, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	m, s, err := Parse(ics, owner, at(day, "12:00"))
	if err != nil {
		t.Fatal(err)
	}
	return m, s
}

func same(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %+v\nwant %+v", what, got, want)
	}
}

func meeting(uid, title, day, from, to string) Meeting {
	return Meeting{UID: uid, Title: title, From: at(day, from), To: at(day, to)}
}

func endOfDay(day string) time.Time { return at(day, "00:00").AddDate(0, 0, 1) }

func TestSkipRules(t *testing.T) {
	const day = "2026-11-10"
	cases := []struct {
		fixture string
		want    []Skipped
	}{
		{"all-day.ics", []Skipped{{Meeting: Meeting{UID: "allday-1", Title: "Team offsite", From: at(day, "00:00"), To: endOfDay(day)}, Reason: AllDay}}},
		{"declined.ics", []Skipped{{Meeting: meeting("declined-1", "Roadmap review", day, "14:00", "15:00"), Reason: Declined}}},
		{"cancelled.ics", []Skipped{{Meeting: meeting("cancelled-1", "Vendor call", day, "10:00", "10:30"), Reason: Cancelled}}},
		{"free.ics", []Skipped{{Meeting: meeting("free-1", "Office hours (optional)", day, "16:00", "17:00"), Reason: Free}}},
		{"alone.ics", []Skipped{
			{Meeting: meeting("alone-focus", "Focus time", day, "08:00", "10:00"), Reason: Alone},
			{Meeting: meeting("alone-room", "Desk booking", day, "11:00", "12:00"), Reason: Alone},
		}},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			m, s := parse(t, c.fixture, day)
			same(t, "meetings", m, []Meeting(nil))
			same(t, "skipped", s, c.want)
		})
	}
}

func TestOwnerWithoutPartstatIsAttending(t *testing.T) {
	m, s := parse(t, "accepted.ics", "2026-11-10")
	same(t, "meetings", m, []Meeting{
		meeting("accepted-organizer", "Design sync", "2026-11-10", "09:00", "09:30"),
		meeting("accepted-one-on-one", "1:1", "2026-11-10", "11:00", "11:30"),
	})
	same(t, "skipped", s, []Skipped(nil))
}

func TestRecurrence(t *testing.T) {
	standup := func(day string) []Meeting { return []Meeting{meeting("standup", "Standup", day, "09:30", "09:45")} }
	cases := []struct {
		name, day string
		meetings  []Meeting
		skipped   []Skipped
	}{
		{"before the DST change", "2026-10-20", standup("2026-10-20"), nil},
		{"after the DST change, same wall time", "2026-11-10", standup("2026-11-10"), nil},
		{"EXDATE", "2026-11-11", nil, nil},
		{"second EXDATE on the same line", "2026-11-16", nil, nil},
		{"RECURRENCE-ID moves it", "2026-11-12", []Meeting{meeting("standup", "Standup (moved)", "2026-11-12", "11:00", "11:30")}, nil},
		{"RECURRENCE-ID cancels one", "2026-11-13", nil, []Skipped{{Meeting: standup("2026-11-13")[0], Reason: Cancelled}}},
		{"weekend", "2026-11-14", nil, nil},
		{"UTC UNTIL keeps the last", "2026-11-20", standup("2026-11-20"), nil},
		{"after UNTIL", "2026-11-23", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, s := parse(t, "recurrence.ics", c.day)
			same(t, "meetings", m, c.meetings)
			same(t, "skipped", s, c.skipped)
		})
	}
}

func TestTimeZones(t *testing.T) {
	const day = "2026-11-10"
	m, s := parse(t, "timezones.ics", day)
	same(t, "meetings", m, []Meeting{
		meeting("tz-utc", "UTC sync", day, "13:00", "13:30"),
		meeting("tz-iana", "New York sync", day, "16:00", "16:30"),
		meeting("tz-windows", "Outlook sync", day, "17:00", "17:30"),
	})
	if len(s) != 1 || s[0].UID != "tz-unknown" || s[0].Reason != Unreadable || !strings.Contains(s[0].Detail, "Nowhere Standard Time") {
		t.Errorf("want tz-unknown skipped as unreadable, got %+v", s)
	}
}

func TestMidnightIsClipped(t *testing.T) {
	m, _ := parse(t, "midnight.ics", "2026-11-10")
	same(t, "first day", m, []Meeting{{UID: "late-1", Title: "Release night", From: at("2026-11-10", "23:00"), To: endOfDay("2026-11-10")}})
	m, _ = parse(t, "midnight.ics", "2026-11-11")
	same(t, "second day", m, []Meeting{meeting("late-1", "Release night", "2026-11-11", "00:00", "01:00")})
}

func TestNotICalendar(t *testing.T) {
	for _, body := range []string{"", "<html>sign in</html>"} {
		if _, _, err := Parse([]byte(body), owner, at("2026-11-10", "12:00")); err == nil {
			t.Errorf("Parse(%q) gave no error", body)
		}
	}
}

func TestAFeedWithoutAttendeesCountsItsEventsAsMeetings(t *testing.T) {
	const day = "2026-11-10"
	m, s := parse(t, "published.ics", day)
	same(t, "meetings", m, []Meeting{
		meeting("pub-standup", "Team standup", day, "08:10", "08:20"),
		meeting("pub-weekly", "Team meeting", day, "12:00", "13:00"),
	})
	same(t, "skipped", s, []Skipped{
		{Meeting: Meeting{UID: "pub-allday", Title: "Public holiday", From: at(day, "00:00"), To: endOfDay(day)}, Reason: AllDay},
		{Meeting: meeting("pub-free", "Lunch", day, "12:30", "13:00"), Reason: Free},
		{Meeting: meeting("pub-cancelled", "Canceled: Vendor call", day, "15:00", "15:30"), Reason: Cancelled},
	})
}
