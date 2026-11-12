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

// meetings reads the calendar for day, when one is connected: once per
// day per run, since the review reloads the day after every change.
func (r *run) meetings(cfg Config, start time.Time) calendar.Day {
	k := start.Format("2006-01-02")
	if d, ok := r.calendars[k]; ok {
		return d
	}
	d := r.fetchMeetings(cfg, start)
	if r.calendars == nil {
		r.calendars = map[string]calendar.Day{}
	}
	r.calendars[k] = d
	return d
}

func (r *run) fetchMeetings(cfg Config, start time.Time) calendar.Day {
	s, err := r.secrets()
	if err != nil {
		return calendar.Day{Source: calendar.None}
	}
	address, _, err := s.Get(calendarSecret)
	if err != nil {
		return calendar.Day{Source: calendar.None}
	}
	dir, err := r.dataDir()
	if err != nil {
		return calendar.Day{Source: calendar.None, Problem: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), calendar.Timeout+time.Second)
	defer cancel()
	return calendar.Load(ctx, nil, dir, address, cfg.Email, start)
}

// calendarNote says where a day's meetings came from.
func calendarNote(c calendar.Day) string {
	switch {
	case c.Problem != "":
		return c.Problem
	case c.Source == calendar.None:
		return "no calendar connected"
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
		ctx, cancel := context.WithTimeout(context.Background(), calendar.Timeout+time.Second)
		defer cancel()
		now := r.Now()
		day := calendar.Load(ctx, nil, dir, address, email, time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()))
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
