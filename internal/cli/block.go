package cli

import (
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/rockylabsdev/sift/internal/draft"
)

var (
	keyArg   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]+-[0-9]+$`)
	rangeArg = regexp.MustCompile(`^(\d{1,2}):(\d{2})[-–](\d{1,2}):(\d{2})$`)
)

// block adds time to a ticket by hand (sift block KEY 10:00-11:30), or
// removes such a block with --remove. A block owns its minutes ahead of
// evidence and meetings.
func (r *run) block(args []string) (any, func(io.Writer), error) {
	var date string
	var remove bool
	var pos []string
	for len(args) > 0 { // flags may come after the key and range
		if !strings.HasPrefix(args[0], "-") {
			pos, args = append(pos, args[0]), args[1:]
			continue
		}
		fs := flag.NewFlagSet("block", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.BoolVar(&r.json, "json", r.json, "")
		fs.StringVar(&date, "date", date, "")
		fs.BoolVar(&remove, "remove", remove, "")
		if err := fs.Parse(args); err != nil {
			return nil, nil, usageError{err.Error()}
		}
		args = fs.Args()
	}
	if len(pos) != 2 || !keyArg.MatchString(pos[0]) {
		return nil, nil, usageError{"usage: sift block KEY HH:MM-HH:MM [--date YYYY-MM-DD] [--remove]"}
	}
	key := strings.ToUpper(pos[0])
	start, _, err := r.day(date)
	if err != nil {
		return nil, nil, err
	}
	g := rangeArg.FindStringSubmatch(pos[1])
	if g == nil {
		return nil, nil, usageError{fmt.Sprintf("the time takes HH:MM-HH:MM, not %q", pos[1])}
	}
	at := func(h, m string) (time.Time, error) {
		t, err := time.ParseInLocation("15:04", fmt.Sprintf("%02s:%s", h, m), start.Location())
		if err != nil {
			return t, usageError{fmt.Sprintf("%s:%s is not a time", h, m)}
		}
		return start.Add(time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute), nil
	}
	from, err := at(g[1], g[2])
	if err != nil {
		return nil, nil, err
	}
	to, err := at(g[3], g[4])
	if err != nil {
		return nil, nil, err
	}
	if !to.After(from) {
		return nil, nil, usageError{"a block ends after it starts"}
	}
	dir, err := r.dataDir()
	if err != nil {
		return nil, nil, err
	}
	b := draft.Block{Ticket: key, From: from, To: to}
	dec, err := draft.Store{Dir: dir}.Update(start.Format("2006-01-02"), func(d *draft.Decisions) error {
		kept := d.Blocks[:0]
		found := false
		for _, x := range d.Blocks {
			if x.Ticket == b.Ticket && x.From.Equal(b.From) && x.To.Equal(b.To) {
				found = true
				continue
			}
			kept = append(kept, x)
		}
		d.Blocks = kept
		if remove && !found {
			return usageError{"no such block"}
		}
		if !remove {
			d.Blocks = append(d.Blocks, b)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	verb := "Added"
	if remove {
		verb = "Removed"
	}
	return map[string]any{"blocks": nonNil(dec.Blocks)}, func(w io.Writer) {
		fmt.Fprintf(w, "%s %s %s–%s (%s) on %s.\n", verb, key, from.Format("15:04"), to.Format("15:04"), dur(int(to.Sub(from)/time.Minute)), start.Format("Mon 2 Jan"))
	}, nil
}
