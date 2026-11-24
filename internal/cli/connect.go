package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/rockylabsdev/sift/internal/calendar"
	"github.com/rockylabsdev/sift/internal/jira"
	"github.com/rockylabsdev/sift/internal/secret"
	"github.com/rockylabsdev/sift/internal/tracker"
	"golang.org/x/term"
)

// Secret names.
const (
	calendarSecret = "calendar-url"
	jiraSecret     = "jira-token:"
)

func (r *run) secrets() (secret.Store, error) { return secret.Open(r.Getenv, runtime.GOOS) }

// client opens the tracker in effect. none reports that there is no
// tracker, so confirmed worklogs are local.
func (r *run) client(cfg Config) (c tracker.Client, none bool, err error) {
	if cfg.Tracker != "jira" {
		return nil, true, nil
	}
	if cfg.Jira.Site == "" || cfg.Jira.Email == "" {
		return nil, false, tracker.ErrNotConfigured
	}
	token := r.Getenv("SIFT_JIRA_TOKEN")
	if token == "" {
		s, err := r.secrets()
		if err != nil {
			return nil, false, err
		}
		token, _, err = s.Get(jiraSecret + cfg.Jira.Email)
		if errors.Is(err, secret.ErrNotFound) {
			return nil, false, tracker.ErrNotConfigured
		}
		if err != nil {
			return nil, false, err
		}
	}
	return jira.New(cfg.Jira.Site, cfg.Jira.Email, token, nil), false, nil
}

// calendarEvery is how often the review refetches the calendar while it
// shows today, or after a fetch failed, so a meeting added after it
// opened still appears.
const calendarEvery = 5 * time.Minute

// errNoCalendar is the download when no calendar is connected.
var errNoCalendar = errors.New("no calendar connected")

// download is one fetch of the feed: the feed, or why there is none.
type download struct {
	feed *calendar.Feed
	err  error
	at   time.Time // when sift tried
}

// fetching is how loading a day gets its calendar.
type fetching int

const (
	fetchNow   fetching = iota // download the feed if this run has none yet
	fetchLater                 // leave it Pending: the review's Refresh fetches it off its UI goroutine
	fetchNever                 // read the day the last download saved: the status hook stays quick
)

// meetings is day's calendar, read from this run's one download of the
// feed, as how says.
func (r *run) meetings(cfg Config, start time.Time, how fetching) calendar.Day {
	k := start.Format("2006-01-02")
	r.calMu.Lock()
	d, ok := r.calendars[k]
	dl := r.feed
	r.calMu.Unlock()
	switch {
	case ok:
		return d
	case dl == nil && how == fetchNever:
		dir, err := r.dataDir()
		if err != nil {
			return calendar.Day{Source: calendar.None}
		}
		return calendar.Saved(dir, start)
	case dl == nil && how == fetchLater:
		return calendar.Day{Source: calendar.Pending}
	case dl == nil:
		dl = r.download(cfg, func(*download) bool { return false })
	}
	return r.dayOf(dl, start, calendar.Unlisted(cfg.NoAttendees))
}

// dayOf reads start from dl, and keeps it while dl is the latest download.
func (r *run) dayOf(dl *download, start time.Time, rule calendar.Unlisted) calendar.Day {
	var d calendar.Day
	dir, err := r.dataDir()
	switch {
	case errors.Is(dl.err, errNoCalendar):
		d = calendar.Day{Source: calendar.None}
	case err != nil:
		d = calendar.Day{Source: calendar.None, Problem: err.Error()}
	default:
		d = calendar.DayOf(dir, dl.feed, dl.err, start, rule)
	}
	r.calMu.Lock()
	defer r.calMu.Unlock()
	if r.feed == dl {
		r.calendars[start.Format("2006-01-02")] = d
	}
	return d
}

// download fetches the feed unless the latest download is fresh enough.
// One download runs at a time, and the lock that Load takes is never held
// while it does.
func (r *run) download(cfg Config, stale func(*download) bool) *download {
	r.fetchMu.Lock()
	defer r.fetchMu.Unlock()
	r.calMu.Lock()
	cur := r.feed
	r.calMu.Unlock()
	if cur != nil && !stale(cur) {
		return cur
	}
	at := r.Now()
	f, err := r.fetch(cfg)
	dl := &download{f, err, at}
	r.calMu.Lock()
	r.feed, r.calendars = dl, map[string]calendar.Day{}
	r.calMu.Unlock()
	return dl
}

// Refresh downloads the feed when the review has none yet, or when the
// last try is calendarEvery old and day is today or that try failed. It
// runs off the review's UI goroutine.
func (s source) Refresh(day time.Time) {
	r := s.r
	if !r.withCalendar || r.demoMeetings != nil {
		return
	}
	cfg, err := r.config()
	if err != nil {
		return
	}
	now := r.Now()
	r.download(cfg, func(d *download) bool {
		return now.Sub(d.at) >= calendarEvery && (inDay(day, now) || d.err != nil)
	})
}

// fetchFeed downloads the connected calendar's feed.
func (r *run) fetchFeed(cfg Config) (*calendar.Feed, error) {
	s, err := r.secrets()
	if err != nil {
		return nil, errNoCalendar
	}
	address, _, err := s.Get(calendarSecret)
	if err != nil || address == "" {
		return nil, errNoCalendar
	}
	return calendar.Fetch(context.Background(), nil, address, cfg.Email, r.Now().Location())
}

// calendarNote says where a day's meetings came from, and how events
// that name no one counted.
func calendarNote(c calendar.Day) string {
	note := calendarCount(c)
	switch {
	case c.Source == calendar.None || c.Source == calendar.Pending || !c.Unlisted || c.Problem != "":
	case c.Bare:
		note += " · published feed without attendees, events count as meetings"
	default:
		note += " · events without attendees count as meetings"
	}
	return note
}

func calendarCount(c calendar.Day) string {
	switch {
	case c.Problem != "":
		return c.Problem
	case c.Source == calendar.None || c.Source == "":
		return "no calendar connected"
	case c.Source == calendar.Pending:
		return "calendar: loading"
	case len(c.Skipped) > 0:
		return fmt.Sprintf("calendar: %s, %d left out", plural(len(c.Meetings), "meeting"), len(c.Skipped))
	}
	return "calendar: " + plural(len(c.Meetings), "meeting")
}

// readSecret reads one line: hidden when stdin is a terminal, so a token
// or address never reaches argv, the screen or shell history.
func (r *run) readSecret(prompt string) (string, error) {
	if f, ok := r.Stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(r.Stderr, prompt)
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(r.Stderr)
		return strings.TrimSpace(string(b)), err
	}
	line, err := bufio.NewReader(r.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// configPath is $XDG_CONFIG_HOME/sift/config.json.
func (r *run) configPath() (string, error) {
	dir := r.Getenv("XDG_CONFIG_HOME")
	if dir == "" && r.Getenv("HOME") != "" {
		dir = filepath.Join(r.Getenv("HOME"), ".config")
	}
	if dir == "" {
		return "", errors.New("neither XDG_CONFIG_HOME nor HOME is set, so sift has no config directory")
	}
	return filepath.Join(dir, "sift", "config.json"), nil
}

// saveConfig rewrites the config file, keeping fields this sift does not know.
func (r *run) saveConfig(change func(map[string]any)) error {
	path, err := r.configPath()
	if err != nil {
		return err
	}
	raw := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &raw); err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
	}
	change(raw)
	b, _ := json.MarshalIndent(raw, "", "  ")
	os.MkdirAll(filepath.Dir(path), 0o700)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// setup connects a tracker or a calendar. Secrets are read from stdin.
//
//	sift setup jira --site https://acme.atlassian.net --email me@acme.com
//	sift setup calendar [--email me@acme.com]
//	sift setup none
func (r *run) setup(args []string) (any, func(io.Writer), error) {
	if len(args) == 0 {
		return nil, nil, usageError{"usage: sift setup jira --site URL --email ADDRESS | calendar [--email ADDRESS] | none"}
	}
	what := args[0]
	r.command = "setup " + what
	var site, email string
	if err := r.flags(args[1:], func(fs *flag.FlagSet) {
		fs.StringVar(&site, "site", "", "")
		fs.StringVar(&email, "email", "", "")
	}); err != nil {
		return nil, nil, err
	}
	s, err := r.secrets()
	if err != nil {
		return nil, nil, err
	}
	switch what {
	case "none":
		err := r.saveConfig(func(c map[string]any) { c["tracker"] = "none" })
		return map[string]any{"tracker": "none"}, func(w io.Writer) {
			fmt.Fprintln(w, "No tracker. Confirmed worklogs are logged locally.")
		}, err
	case "jira":
		if site == "" || email == "" {
			return nil, nil, usageError{"setup jira needs --site and --email"}
		}
		site = strings.TrimRight(site, "/")
		if !strings.Contains(site, "://") {
			site = "https://" + site
		}
		token, err := r.readSecret("Jira API token (hidden): ")
		if err != nil || token == "" {
			return nil, nil, usageError{"setup jira reads the API token from stdin"}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		acct, err := jira.New(site, email, token, nil).TestConnection(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("Jira did not accept these credentials: %w", err)
		}
		src, err := s.Set(jiraSecret+email, token)
		if err != nil {
			return nil, nil, err
		}
		err = r.saveConfig(func(c map[string]any) {
			c["tracker"] = "jira"
			c["jira"] = map[string]any{"site": site, "email": email}
			if _, ok := c["email"]; !ok {
				c["email"] = email
			}
		})
		return map[string]any{"tracker": "jira", "account": acct.DisplayName, "stored": src}, func(w io.Writer) {
			fmt.Fprintf(w, "Connected to %s as %s. Token stored in the %s.\n", site, acct.DisplayName, src)
		}, err
	case "calendar":
		address, err := r.readSecret("Calendar subscription address, iCal or webcal (hidden): ")
		if err != nil || address == "" {
			return nil, nil, usageError{"setup calendar reads the subscription address from stdin"}
		}
		cfg, err := r.config()
		if err != nil {
			return nil, nil, err
		}
		if email == "" {
			email = cfg.Email
		}
		if email == "" {
			return nil, nil, usageError{"setup calendar needs --email, to tell your invitations apart"}
		}
		dir, err := r.dataDir()
		if err != nil {
			return nil, nil, err
		}
		now := r.Now()
		day := calendar.Load(context.Background(), nil, dir, address, email, calendar.Unlisted(cfg.NoAttendees), time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()))
		if day.Source != calendar.Fetched {
			return nil, nil, fmt.Errorf("the calendar could not be read: %s", day.Problem)
		}
		src, err := s.Set(calendarSecret, address)
		if err != nil {
			return nil, nil, err
		}
		err = r.saveConfig(func(c map[string]any) { c["email"] = email })
		return map[string]any{"meetingsToday": len(day.Meetings), "stored": src}, func(w io.Writer) {
			fmt.Fprintf(w, "Calendar connected: %s today, %d left out. Address stored in the %s.\n", plural(len(day.Meetings), "meeting"), len(day.Skipped), src)
		}, err
	}
	return nil, nil, usageError{fmt.Sprintf("setup %q: choose jira, calendar or none", what)}
}
