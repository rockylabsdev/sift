package worklog_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rockylabsdev/sift/internal/jira"
	"github.com/rockylabsdev/sift/internal/tracker"
	"github.com/rockylabsdev/sift/internal/worklog"
)

// A confirmed day goes out through the real Jira client: one POST per
// ticket, a refusal kept as failed in Jira's words.
func TestSyncThroughJira(t *testing.T) {
	var bodies = map[string]map[string]any{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var v map[string]any
		json.Unmarshal(b, &v)
		bodies[r.URL.Path] = v
		if strings.Contains(r.URL.Path, "WEB-2") {
			w.WriteHeader(503)
			w.Write([]byte(`{"errorMessages":["down for maintenance"],"errors":{}}`))
			return
		}
		w.WriteHeader(201)
		w.Write([]byte(`{"id":"110001"}`))
	}))
	defer ts.Close()

	s := worklog.Store{Dir: t.TempDir()}
	started := time.Date(2026, 11, 10, 9, 0, 0, 0, time.FixedZone("CET", 3600))
	if _, err := s.Confirm("2026-11-10", []worklog.Worklog{
		{Ticket: "WEB-1", Started: started, Minutes: 90},
		{Ticket: "WEB-2", Started: started, Minutes: 15},
	}, false); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Sync(context.Background(), jira.New(ts.URL, "me@example.com", "tok", nil), false)
	var refused *tracker.Refusal
	if !errors.As(err, &refused) || refused.Status != 503 {
		t.Fatalf("err = %v, want the 503 as a tracker refusal", err)
	}
	got := bodies["/rest/api/3/issue/WEB-1/worklog"]
	if got["timeSpentSeconds"] != 5400.0 || got["started"] != "2026-11-10T09:00:00.000+0100" {
		t.Errorf("WEB-1 body = %v", got)
	}
	if len(rep.Sent) != 1 || rep.Sent[0].RemoteID != "110001" {
		t.Errorf("sent = %+v", rep.Sent)
	}
	if len(rep.Remaining) != 1 || rep.Remaining[0].Error != "Jira answered 503: down for maintenance" {
		t.Errorf("remaining = %+v", rep.Remaining)
	}
}
