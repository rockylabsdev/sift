// Package worklog is Sift's outbox: one worklog per ticket per confirmed
// day, kept in $dataDir/worklogs.json until the tracker takes it.
//
// Every write (Confirm, and each outcome Sync records) happens under one
// flock on sync.lock (Punchcard ADR 0002). Sync holds it across its POSTs,
// so nothing can change a worklog while it is being sent, and two syncs
// never send the same one. Saves are atomic and synced to disk.
//
// No worklog is sent twice. Before each POST the worklog is saved as
// attempted, and the mark clears only when the outcome is certain: taken,
// refused, or rejected credentials. When the outcome is unknown (Sift died,
// the answer was lost to a timeout, or recording it failed), the next sync
// asks the tracker whether it already holds that worklog (FindWorklog)
// before sending, and records it as synced if it does.
package worklog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rockylabsdev/sift/internal/tracker"
	"golang.org/x/sys/unix"
)

// State is where a worklog stands.
type State string

const (
	// Pending: confirmed, not yet sent.
	Pending State = "pending"
	// Failed: the tracker refused it or could not be reached. Sync retries it.
	Failed State = "failed"
	// Synced: the tracker took it. It is never sent again.
	Synced State = "synced"
	// Local: confirmed with no tracker. Final, owed to no one.
	Local State = "local"
)

func (s State) owed() bool { return s == Pending || s == Failed }

// Worklog is one ticket's time on one day. A worklog is named by its Date
// and Ticket: there is never a second for the same pair.
type Worklog struct {
	Date     string    `json:"date"` // YYYY-MM-DD, the confirmed day
	Ticket   string    `json:"ticket"`
	Started  time.Time `json:"started"`
	Minutes  int       `json:"minutes"`
	Comment  string    `json:"comment,omitempty"`
	State    State     `json:"state"`
	RemoteID string    `json:"remoteId,omitempty"` // the tracker's id, once synced
	Error    string    `json:"error,omitempty"`    // why the last send failed
	SyncedAt time.Time `json:"syncedAt,omitzero"`
	// Attempted: a send began and its outcome is not known. The tracker
	// may hold it, so the next sync looks before sending again.
	Attempted bool `json:"attempted,omitempty"`
}

// Version is the worklogs.json schema this binary writes and reads.
const Version = 1

const (
	FileName = "worklogs.json"
	LockFile = "sync.lock"
	// PostBudget bounds one send.
	PostBudget = 10 * time.Second
	lockWait   = 2 * time.Second
)

// ErrBusy: another sift held sync.lock for the whole wait, so it is
// confirming or sending. Nothing was changed.
var ErrBusy = errors.New("another sift is confirming or sending worklogs")

// Store is the outbox in a data directory.
type Store struct{ Dir string }

type file struct {
	V        int       `json:"v"`
	Worklogs []Worklog `json:"worklogs"`
}

// Confirm records ws as day's worklogs and returns each as it now stands,
// in the order given. Running it again converges: an unsent worklog of
// that day is replaced, one no longer in ws is dropped, and a synced one
// is kept as the tracker has it (it comes back synced, perhaps with other
// minutes than asked). New worklogs are pending, or local when local is
// set. Each w.Date must be day or empty.
func (s Store) Confirm(day string, ws []Worklog, local bool) ([]Worklog, error) {
	if _, err := time.Parse(time.DateOnly, day); err != nil {
		return nil, fmt.Errorf("day %q is not YYYY-MM-DD", day)
	}
	seen := map[string]bool{}
	for _, w := range ws {
		switch {
		case w.Date != "" && w.Date != day:
			return nil, fmt.Errorf("worklog for %s is dated %s, not %s", w.Ticket, w.Date, day)
		case w.Ticket == "":
			return nil, errors.New("a worklog has no ticket")
		case seen[w.Ticket]:
			return nil, fmt.Errorf("two worklogs for %s on %s", w.Ticket, day)
		case w.Minutes <= 0:
			return nil, fmt.Errorf("worklog for %s has %d minutes", w.Ticket, w.Minutes)
		case w.Started.IsZero():
			return nil, fmt.Errorf("worklog for %s has no start", w.Ticket)
		}
		seen[w.Ticket] = true
	}
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	all, err := s.load()
	if err != nil {
		return nil, err
	}
	synced := map[string]Worklog{}
	kept := all[:0]
	for _, w := range all {
		if w.Date != day {
			kept = append(kept, w)
		} else if w.State == Synced || w.Attempted {
			synced[w.Ticket] = w // the tracker has it, or may: keep it as it is
			kept = append(kept, w)
		}
	}
	out := make([]Worklog, 0, len(ws))
	for _, w := range ws {
		if old, ok := synced[w.Ticket]; ok {
			out = append(out, old)
			continue
		}
		w.Date, w.State, w.RemoteID, w.Error, w.SyncedAt = day, Pending, "", "", time.Time{}
		if local {
			w.State = Local
		}
		kept = append(kept, w)
		out = append(out, w)
	}
	byDay(kept)
	return out, s.save(kept)
}

// List is every worklog dated from through to (inclusive, YYYY-MM-DD), by
// date then ticket. It takes no lock: saves are atomic.
func (s Store) List(from, to string) ([]Worklog, error) {
	all, err := s.load()
	if err != nil {
		return nil, err
	}
	var out []Worklog
	for _, w := range all {
		if w.Date >= from && w.Date <= to {
			out = append(out, w)
		}
	}
	return out, nil
}

// Report is what one Sync did.
type Report struct {
	// Sent are the worklogs the tracker took in this run, now synced.
	Sent []Worklog
	// Remaining are the worklogs still owed (pending or failed) after the
	// run. A failed one carries its Error.
	Remaining []Worklog
}

// Sync sends every pending and failed worklog to client, one at a time,
// oldest first, and records each outcome. A refusal marks that worklog
// failed and moves on. A 401 leaves it pending and stops the run, as does
// an unreachable tracker (marked failed), since the rest would fail the
// same way. The error joins what went wrong. With noTracker nothing is
// sent and Remaining says what is still owed from when there was one. A
// nil client with something owed is tracker.ErrNotConfigured.
func (s Store) Sync(ctx context.Context, client tracker.Client, noTracker bool) (Report, error) {
	unlock, err := s.lock()
	if err != nil {
		return Report{}, err
	}
	defer unlock()
	all, err := s.load()
	if err != nil {
		return Report{}, err
	}
	var rep Report
	remaining := func() []Worklog {
		var out []Worklog
		for _, w := range all {
			if w.State.owed() {
				out = append(out, w)
			}
		}
		return out
	}
	owed := remaining()
	switch {
	case noTracker || len(owed) == 0:
		return Report{Remaining: owed}, nil
	case client == nil:
		return Report{Remaining: owed}, tracker.ErrNotConfigured
	}
	var errs []error
	for i := range all {
		w := &all[i]
		if !w.State.owed() {
			continue
		}
		tw := tracker.Worklog{Started: w.Started, TimeSpentSeconds: w.Minutes * 60, Comment: w.Comment}
		if w.Attempted {
			fctx, cancel := context.WithTimeout(ctx, PostBudget)
			id, ferr := client.FindWorklog(fctx, w.Ticket, tw)
			cancel()
			if ferr != nil {
				errs = append(errs, fmt.Errorf("could not check whether %s on %s was already sent, so it was not sent again: %w", w.Ticket, w.Date, ferr))
				break
			}
			if id != "" {
				w.State, w.RemoteID, w.Error, w.SyncedAt, w.Attempted = Synced, id, "", time.Now().UTC().Truncate(time.Second), false
				if err := s.save(all); err != nil {
					errs = append(errs, err)
					break
				}
				rep.Sent = append(rep.Sent, *w)
				continue
			}
		}
		w.Attempted = true
		if err := s.save(all); err != nil {
			w.Attempted = false
			errs = append(errs, fmt.Errorf("%s on %s was not sent: %w", w.Ticket, w.Date, err))
			break
		}
		pctx, cancel := context.WithTimeout(ctx, PostBudget)
		id, perr := client.PostWorklog(pctx, w.Ticket, tw)
		cancel()
		var refused *tracker.Refusal
		stop := false
		switch {
		case perr == nil:
			w.State, w.RemoteID, w.Error, w.SyncedAt, w.Attempted = Synced, id, "", time.Now().UTC().Truncate(time.Second), false
		case errors.Is(perr, tracker.ErrUnauthorized):
			w.Attempted = false // rejected credentials create nothing
			errs = append(errs, perr)
			stop = true
		case errors.As(perr, &refused):
			w.State, w.Error, w.Attempted = Failed, perr.Error(), false
			errs = append(errs, fmt.Errorf("%s on %s: %w", w.Ticket, w.Date, perr))
		default: // no answer: the tracker may have taken it, so it stays attempted
			w.State, w.Error = Failed, perr.Error()
			errs = append(errs, perr)
			stop = true
		}
		if err := s.save(all); err != nil {
			if perr == nil {
				err = fmt.Errorf("the tracker took %s on %s as %s, but it could not be recorded; the next sync finds it there rather than sending it again: %w", w.Ticket, w.Date, id, err)
			}
			errs = append(errs, err)
			stop = true
		} else if perr == nil {
			rep.Sent = append(rep.Sent, *w)
		}
		if stop {
			break
		}
	}
	rep.Remaining = remaining()
	return rep, errors.Join(errs...)
}

func (s Store) load() ([]Worklog, error) {
	path := filepath.Join(s.Dir, FileName)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if f.V > Version {
		return nil, fmt.Errorf("%s is schema v%d, newer than this sift reads (v%d)", path, f.V, Version)
	}
	byDay(f.Worklogs)
	return f.Worklogs, nil
}

func byDay(ws []Worklog) {
	sort.SliceStable(ws, func(i, j int) bool {
		if ws[i].Date != ws[j].Date {
			return ws[i].Date < ws[j].Date
		}
		return ws[i].Ticket < ws[j].Ticket
	})
}

// save writes all atomically: a temp file, synced, renamed over the old
// one, then the directory synced so the rename survives a crash.
func (s Store) save(all []Worklog) error {
	b, err := json.MarshalIndent(file{V: Version, Worklogs: all}, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, "."+FileName+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(append(b, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(s.Dir, FileName))
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("writing %s: %w", FileName, err)
	}
	if d, err := os.Open(s.Dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

func (s Store) lock() (func(), error) {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, LockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
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
