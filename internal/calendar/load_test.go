package calendar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const secret = "private-s3cr3t"

type feed struct {
	srv     *httptest.Server
	address string
	status  int
	body    []byte
}

func newFeed(t *testing.T) *feed {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "accepted.ics"))
	if err != nil {
		t.Fatal(err)
	}
	f := &feed{status: http.StatusOK, body: body}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+secret+"/basic.ics" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(f.status)
		w.Write(f.body)
	}))
	t.Cleanup(f.srv.Close)
	f.address = "webcal://" + strings.TrimPrefix(f.srv.URL, "https://") + "/" + secret + "/basic.ics"
	return f
}

func (f *feed) load(t *testing.T, ctx context.Context, dir string) Day {
	t.Helper()
	d := Load(ctx, f.srv.Client(), dir, f.address, owner, at("2026-11-10", "12:00"))
	if strings.Contains(d.Problem, secret) {
		t.Errorf("problem names the secret address: %q", d.Problem)
	}
	return d
}

var accepted = []Meeting{
	meeting("accepted-organizer", "Design sync", "2026-11-10", "09:00", "09:30"),
	meeting("accepted-one-on-one", "1:1", "2026-11-10", "11:00", "11:30"),
}

func equalMeetings(a, b []Meeting) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].UID != b[i].UID || a[i].Title != b[i].Title || !a[i].From.Equal(b[i].From) || !a[i].To.Equal(b[i].To) {
			return false
		}
	}
	return true
}

func TestLoadFetchesWebcal(t *testing.T) {
	f := newFeed(t)
	d := f.load(t, context.Background(), t.TempDir())
	if d.Source != Fetched || d.Problem != "" || !equalMeetings(d.Meetings, accepted) {
		t.Errorf("got %+v", d)
	}
}

func TestLoadFallsBackToTheDaysCache(t *testing.T) {
	f := newFeed(t)
	dir := t.TempDir()
	f.load(t, context.Background(), dir)

	f.status, f.body = http.StatusInternalServerError, nil
	d := f.load(t, context.Background(), dir)
	if d.Source != Cached || !equalMeetings(d.Meetings, accepted) {
		t.Errorf("got %+v", d)
	}
	if !strings.Contains(d.Problem, "500") {
		t.Errorf("problem does not say why: %q", d.Problem)
	}
}

func TestLoadCacheIsPerDay(t *testing.T) {
	f := newFeed(t)
	dir := t.TempDir()
	f.load(t, context.Background(), dir)

	f.status = http.StatusInternalServerError
	d := Load(context.Background(), f.srv.Client(), dir, f.address, owner, at("2026-11-11", "12:00"))
	if d.Source != None || len(d.Meetings) != 0 {
		t.Errorf("another day's cache was used: %+v", d)
	}
}

func TestLoadFailures(t *testing.T) {
	cases := []struct {
		name   string
		breaks func(f *feed)
		ctx    func() (context.Context, context.CancelFunc)
		says   string
	}{
		{"server error", func(f *feed) { f.status = http.StatusForbidden }, nil, "403"},
		{"not a calendar", func(f *feed) { f.body = []byte("<html>Sign in</html>") }, nil, "not an iCalendar feed"},
		{"unreachable", func(f *feed) { f.srv.Close() }, nil, "could not reach the calendar"},
		{"too slow", func(f *feed) {
			f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		}, func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 50*time.Millisecond)
		}, "took longer than"},
		{"too slow to finish", func(f *feed) {
			f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("BEGIN:VCALENDAR\r\n"))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			})
		}, func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 50*time.Millisecond)
		}, "took longer than"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFeed(t)
			c.breaks(f)
			ctx, cancel := context.Background(), func() {}
			if c.ctx != nil {
				ctx, cancel = c.ctx()
			}
			defer cancel()
			d := f.load(t, ctx, t.TempDir())
			if d.Source != None || len(d.Meetings) != 0 {
				t.Errorf("got %+v", d)
			}
			if !strings.HasPrefix(d.Problem, "Meetings are missing") || !strings.Contains(d.Problem, c.says) {
				t.Errorf("problem %q does not say %q", d.Problem, c.says)
			}
		})
	}
}

func TestLoadWithoutACalendar(t *testing.T) {
	d := Load(context.Background(), nil, t.TempDir(), "", owner, at("2026-11-10", "12:00"))
	if d.Source != None || d.Problem != "" {
		t.Errorf("got %+v", d)
	}
}
