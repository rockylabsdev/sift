package evidence

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Retag says that, in one session, the time logged to ticket Was counts
// as Ticket: the conversation was really about Ticket. The evidence log is
// never rewritten; readers apply retags when they read it.
type Retag struct {
	Session string    `json:"session"`
	Was     string    `json:"was"` // "" is the session's ticketless time
	Ticket  string    `json:"ticket"`
	From    time.Time `json:"from,omitzero"` // zero: the whole session
	At      time.Time `json:"at"`            // when it was made
}

// Retags is the list of retags in a data directory, one JSON line each.
type Retags struct{ Dir string }

func (s Retags) path() string { return filepath.Join(s.Dir, "retags.jsonl") }

// Append adds one retag, under the evidence log's lock.
func (s Retags) Append(rt Retag) error {
	if rt.Session == "" || rt.Ticket == "" {
		return errors.New("a retag names its session and the ticket it moves time to")
	}
	rt.At = rt.At.UTC().Truncate(time.Second)
	if !rt.From.IsZero() {
		rt.From = rt.From.UTC()
	}
	line, err := json.Marshal(rt)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	unlock, err := lockFor(filepath.Join(s.Dir, LockFile), 2*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	f, err := os.OpenFile(s.path(), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Remove takes back the one retag made in session at at, the latest if
// there are two. The others stay as they were, a torn line included.
func (s Retags) Remove(session string, at time.Time) error {
	unlock, err := lockFor(filepath.Join(s.Dir, LockFile), 2*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	b, err := os.ReadFile(s.path())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := bytes.SplitAfter(b, []byte("\n"))
	drop := -1
	for i, l := range lines {
		var rt Retag
		if json.Unmarshal(l, &rt) == nil && rt.Session == session && rt.At.Equal(at) {
			drop = i
		}
	}
	if drop < 0 {
		return fmt.Errorf("no retag of session %s made at %s", session, at.Format(time.RFC3339))
	}
	tmp, err := os.CreateTemp(s.Dir, "retags-*.jsonl")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	for i, l := range lines {
		if i != drop {
			if _, err := tmp.Write(l); err != nil {
				tmp.Close()
				return err
			}
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path())
}

// All reads every retag in the order they were made. Like the log, it
// skips a line it cannot read and reports it.
func (s Retags) All() ([]Retag, []Problem, error) {
	f, err := os.Open(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	var out []Retag
	var problems []Problem
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		var rt Retag
		if err := json.Unmarshal(sc.Bytes(), &rt); err != nil {
			problems = append(problems, Problem{s.path(), n, err.Error()})
			continue
		}
		out = append(out, rt)
	}
	return out, problems, sc.Err()
}

// Apply returns events with each retag applied in order, so a chain (A as
// B, then B as C) moves A's time to C. events is left as it was.
func Apply(events []Event, rs []Retag) []Event {
	if len(rs) == 0 {
		return events
	}
	out := make([]Event, len(events))
	copy(out, events)
	for _, rt := range rs {
		for i := range out {
			e := &out[i]
			t := ""
			if e.Ticket != nil {
				t = *e.Ticket
			}
			if e.Session != rt.Session || t != rt.Was || (!rt.From.IsZero() && e.At.Before(rt.From)) {
				continue
			}
			was := rt.Was
			if was == "" {
				was = "no ticket"
			}
			k := rt.Ticket
			e.Ticket, e.Found = &k, &Found{Via: "retag", Value: was}
		}
	}
	return out
}
