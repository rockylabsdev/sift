// Package cli is the sift command line: the one seam between sift and an
// agent. It keeps punchcard's contract (ADR 0004): an opt-in JSON envelope
// on stdout, exit codes that never use 2, and Origin read from the
// environment.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rockylabsdev/sift/internal/calendar"
	"github.com/rockylabsdev/sift/internal/derive"
	"github.com/rockylabsdev/sift/internal/evidence"
)

// ContractVersion is the envelope's v.
const ContractVersion = 1

// Exit codes, shared with punchcard. 2 is never used: a Claude Code hook
// that exits 2 blocks the user's prompt.
const (
	exitOK       = 0
	exitInternal = 1
	exitUsage    = 64
	exitBusy     = 75
)

var outcomes = map[int]string{exitOK: "ok", exitInternal: "internal", exitUsage: "usage", exitBusy: "busy"}

// Env is everything a run touches outside its arguments.
type Env struct {
	Getenv func(string) string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time
}

// OS is the real environment.
func OS() Env {
	return Env{os.Getenv, os.Stdin, os.Stdout, os.Stderr, time.Now}
}

type envelope struct {
	V       int      `json:"v"`
	OK      bool     `json:"ok"`
	Outcome string   `json:"outcome"`
	Command string   `json:"command"`
	Data    any      `json:"data"`
	Events  []string `json:"events"`
	Error   *string  `json:"error"`
}

// run is one command's context.
type run struct {
	Env
	command  string
	json     bool
	jsonFlag bool     // --json given on the command line, not just SIFT_JSON
	notes    []string // side effects nobody asked for, the envelope's events

	withCalendar bool                                 // fetch meetings when loading a day
	demoMeetings func(day time.Time) []derive.Meeting // the demo's fixed meetings
	fetch        func(Config) (*calendar.Feed, error) // downloads the feed: fetchFeed, or a test's
	fetchMu      sync.Mutex                           // one download at a time
	calMu        sync.Mutex                           // guards feed and calendars; the review downloads off its UI goroutine
	feed         *download                            // this run's latest download; nil before the first ends
	calendars    map[string]calendar.Day              // days read from feed, by date
}

type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

const usage = `sift: a terminal worklog drafter

  sift                               review the earliest day waiting, else today
  sift review [--date YYYY-MM-DD]    review a day: assign, adjust, inspect
  sift status                        today's totals and the days waiting
  sift record [--cwd DIR] [--hook]   record an event: you were working here
  sift events [--date YYYY-MM-DD]    list a day's events
  sift draft  [--date YYYY-MM-DD]    the worklogs the day would produce
  sift block KEY HH:MM-HH:MM         add time by hand (--remove takes it back)
  sift retag KEY --session ID        count a session's time for KEY instead
         [--was KEY|--was-none] [--from HH:MM] [--date YYYY-MM-DD]
  sift retag --list                  the retags made so far
  sift retag --undo --session ID     take back the retag made --at TIME
  sift confirm [--date YYYY-MM-DD]   accept the day: one worklog per ticket, sent
  sift sync                          send worklogs still owed
  sift log [--from D] [--to D]       worklogs, this week by default
  sift setup jira|calendar|none      connect a tracker or a calendar
  sift import punchcard              copy punchcard's worklog history
  sift demo                          review a seeded fake day; nothing is kept
  sift version

  --json (or SIFT_JSON=1) writes the result as a JSON envelope.
  draft --md prints Markdown.`

// Run runs sift with args and returns the exit code.
func Run(args []string, env Env) int {
	r := &run{Env: env, json: env.Getenv("SIFT_JSON") == "1", withCalendar: true}
	r.fetch = r.fetchFeed
	if len(args) == 0 {
		args = []string{"review"}
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(env.Stdout, usage)
		return exitOK
	}
	r.command = args[0]
	for _, a := range args[1:] {
		if a == "--json" || a == "-json" {
			r.json, r.jsonFlag = true, true
		}
	}
	cmds := map[string]func([]string) (any, func(io.Writer), error){
		"record":  r.record,
		"events":  r.events,
		"draft":   r.draft,
		"version": r.version,
		"review":  r.review,
		"status":  r.status,
		"gate":    r.gate,
		"block":   r.block,
		"retag":   r.retag,
		"setup":   r.setup,
		"confirm": r.confirm,
		"sync":    r.sync,
		"log":     r.log,
		"import":  r.importPunchcard,
		"demo":    r.demoCmd,
	}
	cmd, ok := cmds[r.command]
	if !ok {
		return r.finish(nil, nil, usageError{fmt.Sprintf("unknown command %q; run sift help", r.command)})
	}
	data, human, err := cmd(args[1:])
	return r.finish(data, human, err)
}

func (r *run) finish(data any, human func(io.Writer), err error) int {
	code := exitOK
	var u usageError
	var c coded
	switch {
	case err == nil:
	case errors.As(err, &c):
		code = c.code
	case errors.As(err, &u):
		code = exitUsage
	case errors.Is(err, evidence.ErrBusy):
		code = exitBusy
	default:
		code = exitInternal
	}
	if r.json {
		e := envelope{V: ContractVersion, OK: err == nil || code == exitSyncPending, Outcome: outcomes[code], Command: r.command, Data: data, Events: r.notes}
		if e.Events == nil {
			e.Events = []string{}
		}
		if err != nil {
			msg := err.Error()
			e.Error = &msg
		}
		enc := json.NewEncoder(r.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(e)
		return code
	}
	if err != nil && code != exitSyncPending {
		fmt.Fprintln(r.Stderr, "sift:", err)
		return code
	}
	if err != nil {
		defer fmt.Fprintln(r.Stderr, "sift:", err)
	}
	if human != nil {
		human(r.Stdout)
	}
	for _, n := range r.notes {
		fmt.Fprintln(r.Stderr, "sift:", n)
	}
	return code
}

// flags parses a command's flags, adding --json to each.
func (r *run) flags(args []string, define func(*flag.FlagSet)) error {
	fs := flag.NewFlagSet(r.command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&r.json, "json", r.json, "")
	if define != nil {
		define(fs)
	}
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if fs.NArg() > 0 {
		return usageError{fmt.Sprintf("unexpected argument %q", fs.Arg(0))}
	}
	return nil
}

// dataDir is $XDG_DATA_HOME/sift, else ~/.local/share/sift.
func (r *run) dataDir() (string, error) {
	if d := r.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "sift"), nil
	}
	if h := r.Getenv("HOME"); h != "" {
		return filepath.Join(h, ".local", "share", "sift"), nil
	}
	return "", errors.New("neither XDG_DATA_HOME nor HOME is set, so sift has no data directory")
}

// Config is the settings file, $XDG_CONFIG_HOME/sift/config.json.
type Config struct {
	IdleGap   int      `json:"idle_gap_minutes,omitempty"`
	Round     int      `json:"round_minutes,omitempty"` // 0, 5 or 15
	EventsWin bool     `json:"events_beat_meetings,omitempty"`
	Projects  []string `json:"projects,omitempty"` // the branch rule takes only these projects' keys
	Theme     string   `json:"theme,omitempty"`
	Tracker   string   `json:"tracker,omitempty"` // "jira", or "none" (the default): confirm logs worklogs locally
	Jira      struct {
		Site  string `json:"site,omitempty"`
		Email string `json:"email,omitempty"`
	} `json:"jira,omitempty"`
	Email     string `json:"email,omitempty"`      // yours, to tell your calendar invitations apart
	AgentTime string `json:"agent_time,omitempty"` // "fill" (default), "workday" or "off"
}

func (r *run) config() (Config, error) {
	dir := r.Getenv("XDG_CONFIG_HOME")
	if dir == "" && r.Getenv("HOME") != "" {
		dir = filepath.Join(r.Getenv("HOME"), ".config")
	}
	c := Config{IdleGap: derive.Defaults.IdleGap}
	if dir == "" {
		return c, nil
	}
	path := filepath.Join(dir, "sift", "config.json")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %v", path, err)
	}
	switch {
	case c.IdleGap == 0:
		c.IdleGap = derive.Defaults.IdleGap
	case c.IdleGap < 1 || c.IdleGap > 120:
		return c, fmt.Errorf("%s: idle_gap_minutes must be 1 to 120, not %d", path, c.IdleGap)
	}
	switch derive.AgentMode(c.AgentTime) {
	case "", derive.AgentFill, derive.AgentWorkday, derive.AgentOff:
	default:
		return c, fmt.Errorf("%s: agent_time must be fill, workday or off, not %q", path, c.AgentTime)
	}
	if c.Round != 0 && c.Round != 5 && c.Round != 15 {
		return c, fmt.Errorf("%s: round_minutes must be 0, 5 or 15, not %d", path, c.Round)
	}
	return c, nil
}

func (r *run) version(args []string) (any, func(io.Writer), error) {
	if err := r.flags(args, nil); err != nil {
		return nil, nil, err
	}
	v := map[string]any{"version": Version, "contract": ContractVersion, "schema": evidence.Version}
	return v, func(w io.Writer) {
		fmt.Fprintf(w, "sift %s (contract v%d, events v%d)\n", Version, ContractVersion, evidence.Version)
	}, nil
}

// Version is set at build time.
var Version = "dev"
