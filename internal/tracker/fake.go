package tracker

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Fake is a Client with canned outcomes, for tests above the HTTP client.
// The zero value knows no tickets (every Summary is a 404) and accepts
// every worklog.
type Fake struct {
	// Summaries are the tickets the tracker knows, key to summary.
	Summaries map[string]string
	// Err, when set, is what every call returns instead.
	Err error
	// PostErrs, keyed by ticket, is what PostWorklog to that key returns
	// when Err is nil.
	PostErrs map[string]error
	// Account is what TestConnection names.
	Account Account
	// Lose, keyed by ticket, makes PostWorklog keep the worklog but answer
	// ErrUnreachable, as when Jira commits and the answer never arrives.
	Lose map[string]bool

	mu       sync.Mutex
	calls    []string
	worklogs []PostedWorklog
}

// PostedWorklog is one worklog the Fake accepted.
type PostedWorklog struct {
	Key     string
	ID      string
	Worklog Worklog
}

// Calls lists every call made, in order: "summary KEY", "worklog KEY",
// "find KEY", "test".
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// Worklogs lists the worklogs accepted so far. Ids are "W1", "W2", ...
func (f *Fake) Worklogs() []PostedWorklog {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]PostedWorklog(nil), f.worklogs...)
}

func (f *Fake) record(call string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	return f.Err
}

func (f *Fake) Summary(ctx context.Context, key string) (string, error) {
	if err := f.record("summary " + key); err != nil {
		return "", err
	}
	s, ok := f.Summaries[key]
	if !ok {
		return "", &Refusal{Status: 404, Message: "Issue does not exist or you do not have permission to see it."}
	}
	return s, nil
}

func (f *Fake) PostWorklog(ctx context.Context, key string, w Worklog) (string, error) {
	if err := f.record("worklog " + key); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.PostErrs[key]; err != nil {
		return "", err
	}
	id := fmt.Sprintf("W%d", len(f.worklogs)+1)
	f.worklogs = append(f.worklogs, PostedWorklog{Key: key, ID: id, Worklog: w})
	if f.Lose[key] {
		return "", fmt.Errorf("%w: the answer was lost", ErrUnreachable)
	}
	return id, nil
}

func (f *Fake) FindWorklog(ctx context.Context, key string, w Worklog) (string, error) {
	if err := f.record("find " + key); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.worklogs {
		if p.Key == key && p.Worklog.Started.Truncate(time.Second).Equal(w.Started.Truncate(time.Second)) && p.Worklog.TimeSpentSeconds == w.TimeSpentSeconds {
			return p.ID, nil
		}
	}
	return "", nil
}

func (f *Fake) TestConnection(ctx context.Context) (Account, error) {
	if err := f.record("test"); err != nil {
		return Account{}, err
	}
	return f.Account, nil
}
