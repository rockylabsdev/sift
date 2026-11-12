// Package jira is the Jira Cloud tracker (/rest/api/3/, Basic auth with an
// email and a classic API token): one implementation of tracker.Client.
// Only the code that constructs a client imports this package.
package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rockylabsdev/sift/internal/tracker"
)

type client struct {
	base       string // https://<host>/rest/api/3
	email, tok string
	http       *http.Client
}

// New is the client for siteURL (e.g. https://example.atlassian.net; a
// trailing slash is fine). A nil hc is a client that gives up after 30 s;
// callers also bound each call with its context.
func New(siteURL, email, token string, hc *http.Client) tracker.Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &client{base: strings.TrimRight(siteURL, "/") + "/rest/api/3", email: email, tok: token, http: hc}
}

func (c *client) Summary(ctx context.Context, key string) (string, error) {
	var v wireIssue
	if err := c.do(ctx, "GET", "/issue/"+url.PathEscape(key)+"?fields=summary", nil, &v); err != nil {
		return "", err
	}
	return v.Fields.Summary, nil
}

func (c *client) PostWorklog(ctx context.Context, key string, w tracker.Worklog) (string, error) {
	var v wireWorklog
	if err := c.do(ctx, "POST", "/issue/"+url.PathEscape(key)+"/worklog?adjustEstimate=leave", newWireWorklogRequest(w), &v); err != nil {
		return "", err
	}
	return v.ID, nil
}

func (c *client) TestConnection(ctx context.Context) (tracker.Account, error) {
	var si wireServerInfo
	if err := c.do(ctx, "GET", "/serverInfo", nil, &si); err != nil {
		return tracker.Account{}, fmt.Errorf("%w: %w", tracker.ErrBadSite, err)
	}
	if si.DeploymentType != "Cloud" {
		return tracker.Account{}, fmt.Errorf("%w: deployment type %q", tracker.ErrBadSite, si.DeploymentType)
	}
	var me wireMyself
	if err := c.do(ctx, "GET", "/myself", nil, &me); err != nil {
		return tracker.Account{}, err
	}
	return tracker.Account{AccountID: me.AccountID, DisplayName: me.DisplayName, Email: me.EmailAddress}, nil
}

// do sends one request and decodes a 2xx body into out. A transport
// failure is tracker.ErrUnreachable. Any other status is an *APIError.
func (c *client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return fmt.Errorf("%w: %v", tracker.ErrBadSite, err)
	}
	req.SetBasicAuth(c.email, c.tok)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", tracker.ErrUnreachable, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %v", tracker.ErrUnreachable, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e wireError
		_ = json.Unmarshal(b, &e) // an undecodable body leaves the error empty
		return &APIError{Status: resp.StatusCode, Messages: e.ErrorMessages, Fields: e.Errors}
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return nil
}

// APIError is any non-2xx answer, decoded from Jira's error envelope. To
// callers it is a *tracker.Refusal (errors.As), and a 404 matches
// tracker.ErrNotFound and a 401 tracker.ErrUnauthorized.
type APIError struct {
	Status   int
	Messages []string
	Fields   map[string]string
}

func (e *APIError) refusal() *tracker.Refusal {
	parts := append([]string{}, e.Messages...)
	for k, v := range e.Fields {
		parts = append(parts, k+": "+v)
	}
	return &tracker.Refusal{Status: e.Status, Message: strings.Join(parts, "; ")}
}

// Error is the refusal in Jira's words: "Jira answered 503: ...".
func (e *APIError) Error() string {
	r := e.refusal()
	if r.Message == "" {
		return fmt.Sprintf("Jira answered %d", r.Status)
	}
	return fmt.Sprintf("Jira answered %d: %s", r.Status, r.Message)
}

func (e *APIError) Is(target error) bool { return e.refusal().Is(target) }

func (e *APIError) As(target any) bool {
	if r, ok := target.(**tracker.Refusal); ok {
		*r = e.refusal()
		return true
	}
	return false
}
