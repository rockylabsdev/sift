package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rockylabsdev/sift/internal/tracker"
)

// server answers each "METHOD path?query" with a canned status and body,
// and records what it was sent.
type server struct {
	routes map[string]route
	got    []request
}

type route struct {
	status int
	body   []byte
}

type request struct {
	Line, Auth, Body string
}

func newServer(t *testing.T, routes map[string]route) (*server, *httptest.Server) {
	s := &server{routes: routes}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		line := r.Method + " " + r.URL.RequestURI()
		b, _ := io.ReadAll(r.Body)
		s.got = append(s.got, request{line, r.Header.Get("Authorization"), string(b)})
		rt, ok := s.routes[line]
		if !ok {
			t.Errorf("unexpected request %s", line)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rt.status)
		w.Write(rt.body)
	}))
	t.Cleanup(ts.Close)
	return s, ts
}

// basic is base64("user@example.com:tok").
const basic = "Basic dXNlckBleGFtcGxlLmNvbTp0b2s="

func TestSummary(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		want    string
		wantErr error
	}{
		{"200", 200, "issue-summary.json", "Example ticket summary for fixture purposes", nil},
		{"404", 404, "issue-404.json", "", tracker.ErrNotFound},
		{"401", 401, "", "", tracker.ErrUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body []byte
			if c.body != "" {
				body = fixture(t, c.body)
			}
			s, ts := newServer(t, map[string]route{
				"GET /rest/api/3/issue/EX-1234?fields=summary": {c.status, body},
			})
			got, err := New(ts.URL, "user@example.com", "tok", nil).Summary(context.Background(), "EX-1234")
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if got != c.want {
				t.Errorf("summary = %q, want %q", got, c.want)
			}
			if s.got[0].Auth != basic {
				t.Errorf("Authorization = %q", s.got[0].Auth)
			}
		})
	}
}

func TestSummary404KeepsJiraMessage(t *testing.T) {
	_, ts := newServer(t, map[string]route{
		"GET /rest/api/3/issue/EX-9?fields=summary": {404, fixture(t, "issue-404.json")},
	})
	_, err := New(ts.URL, "user@example.com", "tok", nil).Summary(context.Background(), "EX-9")
	var api *APIError
	if !errors.As(err, &api) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if d := diff([]string{"Issue does not exist or you do not have permission to see it."}, api.Messages); d != "" {
		t.Error(d)
	}
}

func TestSummaryUnreachable(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := New(slow.URL, "e", "t", nil).Summary(ctx, "EX-1")
	if !errors.Is(err, tracker.ErrUnreachable) {
		t.Errorf("timeout: err = %v, want tracker.ErrUnreachable", err)
	}

	gone := httptest.NewServer(http.NotFoundHandler())
	url := gone.URL
	gone.Close()
	if _, err := New(url, "e", "t", nil).Summary(context.Background(), "EX-1"); !errors.Is(err, tracker.ErrUnreachable) {
		t.Errorf("closed: err = %v, want tracker.ErrUnreachable", err)
	}
}

func TestPostWorklog(t *testing.T) {
	s, ts := newServer(t, map[string]route{
		"POST /rest/api/3/issue/EX-1234/worklog?adjustEstimate=leave": {201, fixture(t, "worklog-post.json")},
	})
	id, err := New(ts.URL+"/", "user@example.com", "tok", nil).PostWorklog(context.Background(), "EX-1234",
		tracker.Worklog{Started: time.Date(2026, 8, 8, 19, 28, 1, 0, time.UTC), TimeSpentSeconds: 300, Comment: "Punchcard slice-0 spike — delete me"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "110001" {
		t.Errorf("id = %q", id)
	}
	var got, want any
	json.Unmarshal([]byte(s.got[0].Body), &got)
	json.Unmarshal(fixture(t, "request-worklog.json"), &want)
	if d := diff(want, got); d != "" {
		t.Errorf("body (-fixture +sent)\n%s", d)
	}
}

func TestPostWorklogBadStarted(t *testing.T) {
	_, ts := newServer(t, map[string]route{
		"POST /rest/api/3/issue/EX-1/worklog?adjustEstimate=leave": {400, fixture(t, "worklog-post-badstarted.json")},
	})
	_, err := New(ts.URL, "e", "t", nil).PostWorklog(context.Background(), "EX-1", tracker.Worklog{Started: time.Now(), TimeSpentSeconds: 60})
	var api *APIError
	if !errors.As(err, &api) || api.Status != 400 || api.Fields["started"] == "" {
		t.Errorf("err = %#v", err)
	}
}

func TestTestConnection(t *testing.T) {
	cases := []struct {
		name    string
		routes  map[string]route
		want    tracker.Account
		wantErr error
	}{
		{"pass", map[string]route{
			"GET /rest/api/3/serverInfo": {200, fixture(t, "serverinfo.json")},
			"GET /rest/api/3/myself":     {200, fixture(t, "myself.json")},
		}, tracker.Account{AccountID: "712020:11111111-2222-3333-4444-555555555555", DisplayName: "Example User", Email: "user@example.com"}, nil},
		{"bad token", map[string]route{
			"GET /rest/api/3/serverInfo": {200, fixture(t, "serverinfo.json")},
			"GET /rest/api/3/myself":     {401, nil},
		}, tracker.Account{}, tracker.ErrUnauthorized},
		{"not jira", map[string]route{
			"GET /rest/api/3/serverInfo": {404, []byte("<html>")},
		}, tracker.Account{}, tracker.ErrBadSite},
		{"not cloud", map[string]route{
			"GET /rest/api/3/serverInfo": {200, []byte(`{"baseUrl":"x","deploymentType":"Server"}`)},
		}, tracker.Account{}, tracker.ErrBadSite},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ts := newServer(t, c.routes)
			got, err := New(ts.URL, "user@example.com", "tok", nil).TestConnection(context.Background())
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if d := diff(c.want, got); d != "" {
				t.Error(d)
			}
		})
	}
}

// Callers never see *APIError: a Jira refusal is a *tracker.Refusal
// with the status and Jira's words.
func TestAPIErrorIsATrackerRefusal(t *testing.T) {
	_, ts := newServer(t, map[string]route{
		"POST /rest/api/3/issue/EX-1/worklog?adjustEstimate=leave": {400, fixture(t, "worklog-post-badstarted.json")},
	})
	_, err := New(ts.URL, "e", "t", nil).PostWorklog(context.Background(), "EX-1", tracker.Worklog{Started: time.Now(), TimeSpentSeconds: 60})
	var r *tracker.Refusal
	if !errors.As(fmt.Errorf("wrapped: %w", err), &r) {
		t.Fatalf("err = %v, want a *tracker.Refusal", err)
	}
	// The refusal is neutral; the error itself says it in Jira's words.
	if r.Status != 400 || !strings.HasPrefix(r.Message, "started: Invalid date format") ||
		err.Error() != "Jira answered 400: "+r.Message || r.Error() != "the tracker answered 400: "+r.Message {
		t.Errorf("refusal = %#v (%q), err %q", r, r.Error(), err.Error())
	}
}
