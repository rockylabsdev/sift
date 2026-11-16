// Package evidence is the append-only log of events: one JSON object per
// line, a file per month under the data directory. The event's shape is a
// public contract (docs/evidence.md); collectors outside this binary will
// write it.
package evidence

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// Version is the event schema version this binary writes and reads.
const Version = 1

// Origin is who caused an event. Only hand and prompt are evidence.
type Origin string

const (
	Hand   Origin = "hand"
	Prompt Origin = "prompt"
	Agent  Origin = "agent"
)

// IsEvidence reports whether an act of this origin is the user's own work.
func (o Origin) IsEvidence() bool { return o == Hand || o == Prompt }

// Known reports whether the log keeps events of this origin: the user's
// own acts, and an agent's activity, which only fills time the user's
// evidence leaves unclaimed.
func (o Origin) Known() bool { return o.IsEvidence() || o == Agent }

// Found is how an event's ticket was told.
type Found struct {
	Via   string `json:"via"`   // "branch" or "file"
	Value string `json:"value"` // the branch name, or the file's path
}

// Event is one piece of evidence that the user was working.
type Event struct {
	V      int       `json:"v"`
	At     time.Time `json:"at"`
	Source string    `json:"source"` // the collector, e.g. "claude-code"
	Kind   string    `json:"kind"`   // e.g. "prompt"
	Ticket *string   `json:"ticket"` // null when no ticket could be told
	Found  *Found    `json:"found"`  // null exactly when ticket is null
	Dir    string    `json:"dir"`    // the session's directory
	Origin Origin    `json:"origin"`

	Session string `json:"session,omitempty"` // the agent session, when the collector knows it
	AgentID string `json:"agentId,omitempty"` // the subagent, for an agent event from one
	SpanMs  int64  `json:"spanMs,omitempty"`  // how long the act ran, ending at At (a tool call)
}

// ErrNotEvidence: the event's origin is not one the log keeps.
var ErrNotEvidence = errors.New("only hand, prompt and agent origins are evidence")

// ErrBusy: another writer held the lock for the whole wait.
var ErrBusy = errors.New("the evidence log is locked by another sift")

// Log is the evidence log in a data directory.
type Log struct{ Dir string }

// LockFile is the side lock appends take (ADR 0002 in punchcard).
const LockFile = "evidence.lock"

func (l Log) file(t time.Time) string {
	return filepath.Join(l.Dir, "events", t.UTC().Format("2006-01")+".jsonl")
}

// Append writes e as one line. It refuses events that are not evidence.
func (l Log) Append(e Event) error {
	if !e.Origin.Known() {
		return ErrNotEvidence
	}
	if (e.Ticket == nil) != (e.Found == nil) {
		return fmt.Errorf("an event names how its ticket was found exactly when it has one")
	}
	e.V = Version
	e.At = e.At.UTC().Truncate(time.Second)
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	path := l.file(e.At)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	unlock, err := lockFor(filepath.Join(l.Dir, LockFile), 2*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Problem is a line the log could not read. Reading never stops at one.
type Problem struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Err  string `json:"error"`
}

// Between reads every event with from <= At < to, in file order.
func (l Log) Between(from, to time.Time) ([]Event, []Problem, error) {
	var out []Event
	var problems []Problem
	m := time.Date(from.UTC().Year(), from.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	for ; m.Before(to); m = m.AddDate(0, 1, 0) {
		path := l.file(m)
		f, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for n := 1; sc.Scan(); n++ {
			var e Event
			if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
				problems = append(problems, Problem{path, n, err.Error()})
				continue
			}
			if e.V > Version {
				problems = append(problems, Problem{path, n, fmt.Sprintf("schema v%d is newer than this sift reads (v%d)", e.V, Version)})
				continue
			}
			if !e.At.Before(from) && e.At.Before(to) && e.Origin.Known() {
				out = append(out, e)
			}
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, nil, err
		}
	}
	return out, problems, nil
}

func lockFor(path string, wait time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { f.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			f.Close()
			return nil, err
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, ErrBusy
		}
		time.Sleep(20 * time.Millisecond)
	}
}
