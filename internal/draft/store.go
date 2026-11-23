package draft

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Version is the decisions file's schema version. 2 sends a day in parts;
// a sift that reads only 1 would take a sent day for unsent, so it refuses.
const Version = 2

// ErrBusy: another sift held the lock for the whole wait.
var ErrBusy = errors.New("the day is locked by another sift")

// Store keeps one decisions file per day in dir/days.
type Store struct{ Dir string }

func (s Store) path(date string) string { return filepath.Join(s.Dir, "days", date+".json") }

// Load reads a day's decisions; a day never touched has none.
func (s Store) Load(date string) (Decisions, error) {
	dec := Decisions{V: Version, Date: date}
	b, err := os.ReadFile(s.path(date))
	if errors.Is(err, os.ErrNotExist) {
		return dec, nil
	}
	if err != nil {
		return dec, err
	}
	if err := json.Unmarshal(b, &dec); err != nil {
		return dec, fmt.Errorf("%s: %v", s.path(date), err)
	}
	if dec.V > Version {
		return dec, fmt.Errorf("%s was written by a newer sift (v%d)", s.path(date), dec.V)
	}
	if c := dec.Confirmed; c != nil && len(dec.Sent) == 0 {
		day, err := time.ParseInLocation(time.DateOnly, date, time.Local)
		if err != nil {
			return dec, fmt.Errorf("%s: %v", s.path(date), err)
		}
		for i := range c.Worklogs {
			c.Worklogs[i].Raw = c.Worklogs[i].Minutes
		}
		dec.Sent = []Send{{At: c.At, Through: day.AddDate(0, 0, 1), Worklogs: c.Worklogs}}
	}
	dec.Confirmed = nil
	return dec, nil
}

// Update applies change to a day's decisions under the lock and writes the
// result atomically.
func (s Store) Update(date string, change func(*Decisions) error) (Decisions, error) {
	if err := os.MkdirAll(filepath.Join(s.Dir, "days"), 0o700); err != nil {
		return Decisions{}, err
	}
	unlock, err := lock(filepath.Join(s.Dir, "days.lock"), 2*time.Second)
	if err != nil {
		return Decisions{}, err
	}
	defer unlock()
	dec, err := s.Load(date)
	if err != nil {
		return dec, err
	}
	if err := change(&dec); err != nil {
		return dec, err
	}
	dec.V, dec.Date = Version, date
	b, err := json.MarshalIndent(dec, "", "  ")
	if err != nil {
		return dec, err
	}
	tmp := s.path(date) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return dec, err
	}
	return dec, os.Rename(tmp, s.path(date))
}

// Days is every day with decisions, by date.
func (s Store) Days() (map[string]Decisions, error) {
	entries, err := os.ReadDir(filepath.Join(s.Dir, "days"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Decisions{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]Decisions{}
	for _, e := range entries {
		date, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		dec, err := s.Load(date)
		if err != nil {
			return nil, err
		}
		out[date] = dec
	}
	return out, nil
}

func lock(path string, wait time.Duration) (func(), error) {
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
