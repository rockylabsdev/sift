// Package tracker is the seam between Sift and the optional remote a
// worklog is sent to (Punchcard ADR 0008). Jira is one implementation
// (package jira). Callers match the errors here, never an implementation's.
package tracker

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Client is the whole tracker surface Sift uses.
type Client interface {
	// Summary fetches a ticket's summary. A key the tracker does not know,
	// or will not show, is ErrNotFound.
	Summary(ctx context.Context, key string) (string, error)
	// PostWorklog submits one worklog to key and returns the tracker's id
	// for it.
	PostWorklog(ctx context.Context, key string, w Worklog) (string, error)
	// TestConnection checks the site, then the credentials, and names the
	// account.
	TestConnection(ctx context.Context) (Account, error)
}

// Worklog is what PostWorklog tells the tracker. An empty Comment is
// omitted.
type Worklog struct {
	Started          time.Time
	TimeSpentSeconds int
	Comment          string
}

// Account is who the credentials authenticate as.
type Account struct {
	AccountID   string
	DisplayName string
	Email       string
}

// Match with errors.Is, and *Refusal with errors.As.
var (
	// ErrNotConfigured: a tracker is chosen but there is no client to send
	// with (a field or the token is missing).
	ErrNotConfigured = errors.New("the tracker is not set up")
	ErrNotFound      = errors.New("not found or not visible to this account")
	ErrUnauthorized  = errors.New("credentials rejected by the tracker")
	// ErrUnreachable: no answer at all (network, DNS, timeout).
	ErrUnreachable = errors.New("could not reach the tracker")
	ErrBadSite     = errors.New("not a site this tracker can reach")
)

// Refusal is any other answer the tracker gave: an HTTP-like status and
// what it said. A 404 matches ErrNotFound and a 401 ErrUnauthorized.
type Refusal struct {
	Status  int
	Message string
}

func (r *Refusal) Error() string {
	if r.Message == "" {
		return fmt.Sprintf("the tracker answered %d", r.Status)
	}
	return fmt.Sprintf("the tracker answered %d: %s", r.Status, r.Message)
}

func (r *Refusal) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return r.Status == 404
	case ErrUnauthorized:
		return r.Status == 401
	}
	return false
}
