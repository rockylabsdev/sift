package jira

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rockylabsdev/sift/internal/tracker"
)

func TestFindWorklogMatchesMineByStartAndDuration(t *testing.T) {
	var query string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/myself":
			io.WriteString(w, `{"accountId":"me-1","displayName":"Me"}`)
		case "/rest/api/3/issue/PUN-142/worklog":
			query = r.URL.RawQuery
			io.WriteString(w, `{"worklogs":[
			  {"id":"1","started":"2026-11-10T10:00:00.000+0100","timeSpentSeconds":5400,"author":{"accountId":"someone-else"}},
			  {"id":"2","started":"2026-11-10T10:00:00.000+0100","timeSpentSeconds":600,"author":{"accountId":"me-1"}},
			  {"id":"3","started":"2026-11-10T09:00:00.000+0000","timeSpentSeconds":5400,"author":{"accountId":"me-1"}}
			]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	c := New(ts.URL, "me@acme.test", "tok", nil)
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	id, err := c.FindWorklog(context.Background(), "PUN-142", tracker.Worklog{Started: at, TimeSpentSeconds: 5400})
	if err != nil || id != "3" {
		t.Fatalf("id %q err %v; want 3 (mine, same instant in another offset, same duration)", id, err)
	}
	if !strings.Contains(query, "startedAfter=") || !strings.Contains(query, "startedBefore=") {
		t.Errorf("the query asks for every worklog on the issue: %s", query)
	}
	id, err = c.FindWorklog(context.Background(), "PUN-142", tracker.Worklog{Started: at, TimeSpentSeconds: 60})
	if err != nil || id != "" {
		t.Fatalf("no match: id %q err %v", id, err)
	}
}
