package draft

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Version is the decisions file's schema version.
const Version = 1

// ErrConfirmed: the day was confirmed and is read-only.
var ErrConfirmed = errors.New("this day is confirmed and can no longer change")

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
	return dec, nil
}

// Update applies change to a day's decisions under the lock and writes the
// result atomically. A confirmed day refuses every change.
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
	if dec.Confirmed != nil {
		return dec, ErrConfirmed
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

// Days lists every day with decisions, and whether it is confirmed.
func (s Store) Days() (map[string]bool, error) {
	entries, err := os.ReadDir(filepath.Join(s.Dir, "days"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, e := range entries {
		date, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		dec, err := s.Load(date)
		if err != nil {
			return nil, err
		}
		out[date] = dec.Confirmed != nil
	}
	return out, nil
}

// SortedDates is m's keys in order.
func SortedDates(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
