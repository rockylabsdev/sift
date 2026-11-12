package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rockylabsdev/sift/internal/draft"
	"github.com/rockylabsdev/sift/internal/evidence"
	"github.com/rockylabsdev/sift/internal/tui"
	"golang.org/x/term"
)

// lookback is how far back sift looks for days still waiting for review.
const lookback = 14

// source is the review screen's view of the CLI's data.
type source struct{ r *run }

func (s source) Load(day time.Time) (tui.Input, error) {
	l, err := s.r.load(day.Format("2006-01-02"))
	if err != nil {
		return tui.Input{}, err
	}
	return tui.Input{Start: l.Start, Events: l.Events, Day: l.Day, Decisions: l.Decisions,
		Round: l.Config.Round, IdleGap: l.Config.IdleGap, Meetings: "no calendar connected"}, nil
}

func (s source) Update(day time.Time, change func(*draft.Decisions) error) error {
	dir, err := s.r.dataDir()
	if err != nil {
		return err
	}
	_, err = draft.Store{Dir: dir}.Update(day.Format("2006-01-02"), change)
	return err
}

// waiting lists the days before today with evidence that are not confirmed.
func (r *run) waiting() ([]string, error) {
	dir, err := r.dataDir()
	if err != nil {
		return nil, err
	}
	now := r.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	events, _, err := evidence.Log{Dir: dir}.Between(today.AddDate(0, 0, -lookback), today)
	if err != nil {
		return nil, err
	}
	days, err := draft.Store{Dir: dir}.Days()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, e := range events {
		d := e.At.In(now.Location()).Format("2006-01-02")
		if !days[d] {
			seen[d] = true
		}
	}
	var out []string
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}

func (r *run) review(args []string) (any, func(io.Writer), error) {
	var date, theme string
	if err := r.flags(args, func(fs *flag.FlagSet) {
		fs.StringVar(&date, "date", "", "")
		fs.StringVar(&theme, "theme", r.Getenv("SIFT_THEME"), "")
	}); err != nil {
		return nil, nil, err
	}
	if r.json {
		return nil, nil, usageError{"review is a screen; use draft --json"}
	}
	f, ok := r.Stdout.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return nil, nil, usageError{"review needs a terminal; use sift draft"}
	}
	now := r.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if date != "" {
		d, err := time.ParseInLocation("2006-01-02", date, now.Location())
		if err != nil {
			return nil, nil, usageError{fmt.Sprintf("--date takes YYYY-MM-DD, not %q", date)}
		}
		day = d
	} else if w, err := r.waiting(); err == nil && len(w) > 0 {
		day, _ = time.ParseInLocation("2006-01-02", w[0], now.Location())
	}
	cfg, err := r.config()
	if err != nil {
		return nil, nil, err
	}
	if theme == "" {
		theme = cfg.Theme
	}
	_, err = tea.NewProgram(tui.New(source{r}, day, theme)).Run()
	return nil, nil, err
}

func (r *run) status(args []string) (any, func(io.Writer), error) {
	if err := r.flags(args, nil); err != nil {
		return nil, nil, err
	}
	l, err := r.load("")
	if err != nil {
		return nil, nil, err
	}
	d := draft.Of(l.Day, l.Decisions, l.Config.Round)
	w, err := r.waiting()
	if err != nil {
		return nil, nil, err
	}
	data := map[string]any{"today": d, "waiting": nonNil(w)}
	return data, func(out io.Writer) {
		fmt.Fprintf(out, "Today %s", dur(d.Total))
		for _, wl := range d.Worklogs {
			fmt.Fprintf(out, " · %s %s", wl.Ticket, dur(wl.Minutes))
		}
		fmt.Fprintln(out)
		if len(w) > 0 {
			fmt.Fprintf(out, "%s waiting for review, from %s. Run sift.\n", plural(len(w), "day"), w[0])
		}
	}, nil
}
