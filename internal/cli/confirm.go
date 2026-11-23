package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/rockylabsdev/sift/internal/draft"
	"github.com/rockylabsdev/sift/internal/tracker"
	"github.com/rockylabsdev/sift/internal/worklog"
)

// More exit codes from punchcard's contract (ADR 0004).
const (
	exitNotConfigured     = 3
	exitNeedsConfirmation = 6
	exitSyncPending       = 7
	exitAuthFailed        = 8
)

func init() {
	outcomes[exitNotConfigured] = "not_configured"
	outcomes[exitNeedsConfirmation] = "needs_confirmation"
	outcomes[exitSyncPending] = "sync_pending"
	outcomes[exitAuthFailed] = "auth_failed"
}

// coded is an error with its own exit code. Data still goes out with it.
type coded struct {
	code int
	msg  string
}

func (c coded) Error() string { return c.msg }

func (r *run) worklogs() (worklog.Store, error) {
	dir, err := r.dataDir()
	return worklog.Store{Dir: dir}, err
}

// Confirmation is what confirm reports.
type Confirmation struct {
	Date      string            `json:"date"`
	Worklogs  []worklog.Worklog `json:"worklogs"` // the day's, every part
	Sent      int               `json:"sent"`
	Remaining []worklog.Worklog `json:"remaining"`
	Already   bool              `json:"already,omitempty"` // nothing new since the last send
	Minutes   int               `json:"minutes"`           // what this confirm sent
	Through   time.Time         `json:"through,omitzero"`  // the watermark it sent through
}

// confirmDay sends what date has ready since its last send, one worklog
// per ticket of at least a minute, as the day's next part, and sets the
// watermark: time before it is sent and stays as it was. Later work builds
// a new draft. It is safe to repeat: with nothing new it sends nothing.
func (r *run) confirmDay(date string) (Confirmation, error) {
	l, err := r.load(date)
	if err != nil {
		return Confirmation{}, err
	}
	key := l.Start.Format("2006-01-02")
	c := Confirmation{Date: key}
	dr := draft.Of(l.Day, l.Decisions, l.Config.Round)
	client, none, cerr := r.client(l.Config)
	store, err := r.worklogs()
	if err != nil {
		return c, err
	}
	ready := dr.Sendable()
	if len(ready) == 0 {
		c.Already, c.Through = true, dr.Through
	} else {
		now := r.Now()
		part := len(l.Decisions.Sent)
		// the draft counts the minute now is in, so the watermark is the next
		c.Through = minTime(now.Truncate(time.Minute).Add(time.Minute), l.Start.AddDate(0, 0, 1))
		var ws []worklog.Worklog
		for _, w := range ready {
			ws = append(ws, worklog.Worklog{Date: key, Ticket: w.Ticket, Started: w.Started, Minutes: w.Minutes})
			c.Minutes += w.Minutes
		}
		if _, err := store.Confirm(key, part, ws, none); err != nil {
			return c, err
		}
		if _, err := l.Store.Update(key, func(d *draft.Decisions) error {
			if len(d.Sent) > part {
				return nil // another confirm recorded this part first
			}
			d.Sent = append(d.Sent, draft.Send{At: now.UTC().Truncate(time.Second), Through: c.Through, Worklogs: dr.Tally()})
			d.Adjust = nil // what it changed was sent
			return nil
		}); err != nil {
			return c, err
		}
	}
	c.Worklogs, err = store.List(key, key)
	if err != nil {
		return c, err
	}
	rep, serr := r.send(store, client, none, cerr)
	c.Sent, c.Remaining = len(rep.Sent), nonNil(rep.Remaining)
	if serr != nil {
		return c, serr
	}
	c.Worklogs, err = store.List(key, key)
	return c, err
}

// send syncs the outbox and turns what is left into an exit code.
func (r *run) send(store worklog.Store, client tracker.Client, none bool, cerr error) (worklog.Report, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if cerr != nil {
		rep, _ := store.Sync(ctx, nil, true)
		if errors.Is(cerr, tracker.ErrNotConfigured) {
			return rep, coded{exitNotConfigured, "Jira is not set up: run sift setup jira, then sift sync"}
		}
		return rep, cerr
	}
	rep, err := store.Sync(ctx, client, none)
	switch {
	case errors.Is(err, tracker.ErrUnauthorized):
		return rep, coded{exitAuthFailed, "Jira rejected the credentials: run sift setup jira again, then sift sync"}
	case len(rep.Remaining) > 0 && !none:
		msg := fmt.Sprintf("%s not sent yet; run sift sync", plural(len(rep.Remaining), "worklog"))
		if err != nil {
			msg += ": " + err.Error()
		}
		return rep, coded{exitSyncPending, msg}
	}
	return rep, err
}

func (r *run) confirm(args []string) (any, func(io.Writer), error) {
	var date string
	var yes bool
	if err := r.flags(args, func(fs *flag.FlagSet) {
		fs.StringVar(&date, "date", "", "")
		fs.BoolVar(&yes, "yes", false, "")
	}); err != nil {
		return nil, nil, err
	}
	if r.origin() == "agent" && !yes {
		l, err := r.load(date)
		if err != nil {
			return nil, nil, err
		}
		preview := draft.Of(l.Day, l.Decisions, l.Config.Round)
		return map[string]any{"preview": preview}, nil, coded{exitNeedsConfirmation,
			"an agent's confirm is a proposal: show the user this draft; sift confirm --yes asks them before anything is sent"}
	}
	c, err := r.confirmDay(date)
	return c, func(w io.Writer) { writeConfirmation(w, c) }, err
}

func writeConfirmation(w io.Writer, c Confirmation) {
	total := 0
	for _, x := range c.Worklogs {
		total += x.Minutes
	}
	switch {
	case c.Already && len(c.Worklogs) == 0:
		fmt.Fprintf(w, "Nothing to send on %s.\n", c.Date)
	case c.Already:
		fmt.Fprintf(w, "Nothing new to send on %s since %s. Sent so far: %s, %s.\n", c.Date, c.Through.Local().Format("15:04"), plural(len(c.Worklogs), "worklog"), dur(total))
	default:
		fmt.Fprintf(w, "Sent %s of %s through %s. The day so far: %s, %s.\n", dur(c.Minutes), c.Date, c.Through.Local().Format("15:04"), plural(len(c.Worklogs), "worklog"), dur(total))
	}
	for _, x := range c.Worklogs {
		fmt.Fprintf(w, "  %-10s %8s  %s\n", x.Ticket, dur(x.Minutes), x.State)
	}
	if c.Sent > 0 {
		fmt.Fprintf(w, "%s sent.\n", plural(c.Sent, "worklog"))
	}
}

func (r *run) sync(args []string) (any, func(io.Writer), error) {
	if err := r.flags(args, nil); err != nil {
		return nil, nil, err
	}
	cfg, err := r.config()
	if err != nil {
		return nil, nil, err
	}
	store, err := r.worklogs()
	if err != nil {
		return nil, nil, err
	}
	client, none, cerr := r.client(cfg)
	rep, err := r.send(store, client, none, cerr)
	data := map[string]any{"sent": nonNil(rep.Sent), "remaining": nonNil(rep.Remaining)}
	return data, func(w io.Writer) {
		switch {
		case len(rep.Sent) > 0:
			fmt.Fprintf(w, "%s sent.\n", plural(len(rep.Sent), "worklog"))
		case len(rep.Remaining) == 0:
			fmt.Fprintln(w, "Nothing to send.")
		}
	}, err
}

// Imported is a worklog punchcard logged before sift.
type Imported struct {
	Date     string    `json:"date"`
	Started  time.Time `json:"started"`
	Ticket   string    `json:"key"`
	Minutes  int       `json:"minutes"`
	Comment  string    `json:"comment"`
	State    string    `json:"state"`
	RemoteID string    `json:"remoteId"`
}

func (r *run) importedPath() (string, error) {
	dir, err := r.dataDir()
	return filepath.Join(dir, "imported", "punchcard.json"), err
}

// importPunchcard copies punchcard's worklog history so sift log answers
// for past weeks. It reads punchcard's own log contract and is safe to repeat.
func (r *run) importPunchcard(args []string) (any, func(io.Writer), error) {
	if len(args) == 0 || args[0] != "punchcard" {
		return nil, nil, usageError{"usage: sift import punchcard"}
	}
	r.command = "import punchcard"
	if err := r.flags(args[1:], nil); err != nil {
		return nil, nil, err
	}
	bin := r.Getenv("SIFT_PUNCHCARD")
	if bin == "" {
		bin = "punchcard"
	}
	cmd := exec.Command(bin, "log", "--json", "--from", "2000-01-01")
	cmd.Env = append(os.Environ(), "CLAUDECODE=", "AI_AGENT=")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, nil, fmt.Errorf("punchcard log: %v", err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Rows []Imported `json:"rows"`
		} `json:"data"`
		Error *string `json:"error"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, nil, fmt.Errorf("punchcard log: %v", err)
	}
	if !env.OK {
		msg := "failed"
		if env.Error != nil {
			msg = *env.Error
		}
		return nil, nil, fmt.Errorf("punchcard log: %s", msg)
	}
	owed := 0
	for _, x := range env.Data.Rows {
		if x.State == "pending" || x.State == "failed" {
			owed++
		}
	}
	path, err := r.importedPath()
	if err != nil {
		return nil, nil, err
	}
	b, _ := json.MarshalIndent(env.Data.Rows, "", "  ")
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path+".tmp", b, 0o600); err != nil {
		return nil, nil, err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return nil, nil, err
	}
	if owed > 0 {
		r.notes = append(r.notes, fmt.Sprintf("%s still owed in punchcard; send them with punchcard sync, then import again", plural(owed, "worklog")))
	}
	return map[string]any{"imported": len(env.Data.Rows), "owedInPunchcard": owed}, func(w io.Writer) {
		fmt.Fprintf(w, "Imported %s from punchcard.\n", plural(len(env.Data.Rows), "worklog"))
	}, nil
}

// LogRow is one worklog in sift log.
type LogRow struct {
	Date    string `json:"date"`
	Ticket  string `json:"ticket"`
	Minutes int    `json:"minutes"`
	State   string `json:"state"`
	From    string `json:"from"` // "sift" or "punchcard"
}

func (r *run) log(args []string) (any, func(io.Writer), error) {
	var from, to string
	if err := r.flags(args, func(fs *flag.FlagSet) {
		fs.StringVar(&from, "from", "", "")
		fs.StringVar(&to, "to", "", "")
	}); err != nil {
		return nil, nil, err
	}
	now := r.Now()
	if to == "" {
		to = now.Format("2006-01-02")
	}
	if from == "" {
		monday := now.AddDate(0, 0, -((int(now.Weekday()) + 6) % 7))
		from = monday.Format("2006-01-02")
	}
	store, err := r.worklogs()
	if err != nil {
		return nil, nil, err
	}
	ws, err := store.List(from, to)
	if err != nil {
		return nil, nil, err
	}
	var rows []LogRow
	for _, w := range ws {
		rows = append(rows, LogRow{w.Date, w.Ticket, w.Minutes, string(w.State), "sift"})
	}
	if path, err := r.importedPath(); err == nil {
		if b, err := os.ReadFile(path); err == nil {
			var old []Imported
			json.Unmarshal(b, &old)
			for _, x := range old {
				if x.Date >= from && x.Date <= to {
					rows = append(rows, LogRow{x.Date, x.Ticket, x.Minutes, x.State, "punchcard"})
				}
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Date < rows[j].Date })
	return map[string]any{"rows": nonNil(rows), "from": from, "to": to}, func(w io.Writer) {
		if len(rows) == 0 {
			fmt.Fprintf(w, "No worklogs from %s to %s.\n", from, to)
			return
		}
		total := 0
		for _, x := range rows {
			src := ""
			if x.From == "punchcard" {
				src = "  (punchcard)"
			}
			fmt.Fprintf(w, "%s  %-10s %8s  %s%s\n", x.Date, x.Ticket, dur(x.Minutes), x.State, src)
			total += x.Minutes
		}
		fmt.Fprintf(w, "%-10s  %-10s %8s\n", "total", "", dur(total))
	}, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
