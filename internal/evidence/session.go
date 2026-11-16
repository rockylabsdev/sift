package evidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Sessions remembers, per agent session, the ticket of the user's latest
// prompt, so a subagent working in a worktree whose branch names no ticket
// is credited to the ticket of the session that started it. It also
// paces agent events.
type Sessions struct{ Dir string }

var sessionRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type sessionState struct {
	Ticket *string `json:"ticket"`
	Found  *Found  `json:"found"`
}

func (s Sessions) path(id, suffix string) (string, bool) {
	if !sessionRe.MatchString(id) {
		return "", false
	}
	return filepath.Join(s.Dir, "sessions", id+suffix), true
}

// Remember records the ticket of the session's latest prompt.
func (s Sessions) Remember(id string, ticket *string, found *Found) error {
	p, ok := s.path(id, ".json")
	if !ok {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(sessionState{ticket, found})
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Recall is the ticket of the session's latest prompt, if it had one.
func (s Sessions) Recall(id string) (*string, *Found) {
	p, ok := s.path(id, ".json")
	if !ok {
		return nil, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, nil
	}
	var st sessionState
	if json.Unmarshal(b, &st) != nil || st.Ticket == nil {
		return nil, nil
	}
	return st.Ticket, &Found{Via: "session", Value: id}
}

// Due reports whether an agent event for this session and ticket should be
// written at now: at most one a minute, since the deriver counts minutes.
// It marks the time when it says yes.
func (s Sessions) Due(id, ticket string, now time.Time) bool {
	p, ok := s.path(id, "."+keyName(ticket)+".agent")
	if !ok {
		return true
	}
	if fi, err := os.Stat(p); err == nil && now.Sub(fi.ModTime()) < time.Minute && now.Sub(fi.ModTime()) >= 0 {
		return false
	}
	os.MkdirAll(filepath.Dir(p), 0o700)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		f.Close()
	}
	os.Chtimes(p, now, now)
	return true
}

func keyName(ticket string) string {
	if ticket == "" || !sessionRe.MatchString(ticket) {
		return "none"
	}
	return ticket
}
