package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rockylabsdev/sift/internal/demo"
	"github.com/rockylabsdev/sift/internal/derive"
	"github.com/rockylabsdev/sift/internal/evidence"
)

// demoCmd opens the review on yesterday as a seeded fake day, in a
// throwaway data directory with no tracker, so nothing real is touched.
func (r *run) demoCmd(args []string) (any, func(io.Writer), error) {
	if err := r.flags(args, nil); err != nil {
		return nil, nil, err
	}
	tmp, err := os.MkdirTemp("", "sift-demo")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)
	now := r.Now()
	day := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, now.Location())
	events, meetings := demo.Day(day)
	log := evidence.Log{Dir: tmp + "/data/sift"}
	for _, e := range events {
		if err := log.Append(e); err != nil {
			return nil, nil, err
		}
	}
	real := r.Getenv
	r.Getenv = func(k string) string {
		switch k {
		case "XDG_DATA_HOME":
			return tmp + "/data"
		case "XDG_CONFIG_HOME":
			return tmp + "/config"
		case "SIFT_JIRA_TOKEN":
			return ""
		}
		return real(k)
	}
	r.withCalendar = false
	r.demoMeetings = func(d time.Time) []derive.Meeting {
		if d.Equal(day) {
			return meetings
		}
		return nil
	}
	_, _, err = r.review([]string{"--date", day.Format("2006-01-02")})
	return nil, func(w io.Writer) { fmt.Fprintln(w, "The demo kept nothing. Run sift to review your own days.") }, err
}
