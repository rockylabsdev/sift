package cli

import (
	"flag"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rockylabsdev/sift/internal/draft"
	"github.com/rockylabsdev/sift/internal/evidence"
)

var (
	retagPromptRe = regexp.MustCompile(`(?s)^\s*/sift(?::sift)?\s+(?i:retag)\b(.*)$`)
	retagArgsRe   = regexp.MustCompile(`(?i)^\s*([a-z][a-z0-9]+-[0-9]+)(?:\s+from\s+(\d{1,2}):(\d{2}))?\s*$`)
	clockRe       = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
)

const retagHow = "Sift changed nothing: to count this conversation's time for another ticket, the user types /sift retag KEY (for example /sift retag PUN-160), or /sift retag KEY from HH:MM to move only the time from then on."

// clockOn is hh:mm on day, false when that is not a time.
func clockOn(day time.Time, hh, mm string) (time.Time, bool) {
	h, _ := strconv.Atoi(hh)
	m, _ := strconv.Atoi(mm)
	if h > 23 || m > 59 {
		return time.Time{}, false
	}
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, day.Location()), true
}

// saveRetag makes key the session's ticket in place of was from from on
// (zero: the whole session). was is first read through the retags already
// made, so retagging twice moves the time on rather than leaving it behind.
// It saves nothing, and says so, when the session already counts for key.
func saveRetag(dir, session string, was *string, key string, from, now time.Time) (evidence.Retag, bool, error) {
	store := evidence.Retags{Dir: dir}
	rs, _, err := store.All()
	if err != nil {
		return evidence.Retag{}, false, err
	}
	e := evidence.Apply([]evidence.Event{{Session: session, Ticket: was, At: now}}, rs)[0]
	rt := evidence.Retag{Session: session, Ticket: key, From: from, At: now}
	if e.Ticket != nil {
		rt.Was = *e.Ticket
	}
	if rt.Was == key {
		return rt, false, nil
	}
	return rt, true, store.Append(rt)
}

func timeOf(was string) string {
	if was == "" {
		return "time with no ticket"
	}
	return was + " time"
}

// retagPrompt answers a /sift retag prompt the hook just recorded as e:
// the session's time on e's ticket counts for the key typed. It returns
// the sentence to add to Claude's context, or "" for any other prompt.
func (r *run) retagPrompt(dir string, in hookInput, e evidence.Event) string {
	m := retagPromptRe.FindStringSubmatch(in.Prompt)
	if m == nil {
		return ""
	}
	a := retagArgsRe.FindStringSubmatch(m[1])
	if a == nil {
		return retagHow
	}
	var from time.Time
	if a[2] != "" {
		var ok bool
		if from, ok = clockOn(e.At, a[2], a[3]); !ok {
			return retagHow
		}
	}
	if in.Session == "" {
		return "Sift could not tell which conversation this is (Claude Code sent no session id), so it changed nothing."
	}
	key := strings.ToUpper(a[1])
	rt, saved, err := saveRetag(dir, in.Session, e.Ticket, key, from, e.At)
	if err != nil {
		return "Sift could not save the retag: " + err.Error()
	}
	if !saved {
		return fmt.Sprintf("Sift: this conversation is already logged to %s; nothing to change.", key)
	}
	since := ""
	if !from.IsZero() {
		since = " from " + from.Format("15:04") + " on"
	}
	msg := fmt.Sprintf("Sift moved this conversation's %s%s to %s, and will keep doing so.", timeOf(rt.Was), since, key)
	r.withCalendar = false
	l, err := r.load("")
	if err != nil {
		return msg
	}
	if l.Decisions.Confirmed != nil {
		return msg + " Today is already confirmed, so today's worklogs stay as they were sent."
	}
	for _, w := range draft.Of(l.Day, l.Decisions, l.Config.Round).Worklogs {
		if w.Ticket == key {
			msg += fmt.Sprintf(" %s now has %s today.", key, dur(w.Minutes))
		}
	}
	return msg
}

// retag is sift retag for scripts: the same as typing /sift retag in the
// session, with the session named. Without --was, the time moved is that
// of the ticket the session's latest prompt was logged to.
func (r *run) retag(args []string) (any, func(io.Writer), error) {
	var session, was, from, date, made string
	var wasNone, list, undo bool
	var pos []string
	for len(args) > 0 { // flags may come after the key
		if !strings.HasPrefix(args[0], "-") {
			pos, args = append(pos, args[0]), args[1:]
			continue
		}
		fs := flag.NewFlagSet("retag", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.BoolVar(&r.json, "json", r.json, "")
		fs.StringVar(&session, "session", session, "")
		fs.StringVar(&was, "was", was, "")
		fs.BoolVar(&wasNone, "was-none", wasNone, "")
		fs.StringVar(&from, "from", from, "")
		fs.StringVar(&date, "date", date, "")
		fs.BoolVar(&list, "list", list, "")
		fs.BoolVar(&undo, "undo", undo, "")
		fs.StringVar(&made, "at", made, "")
		if err := fs.Parse(args); err != nil {
			return nil, nil, usageError{err.Error()}
		}
		args = fs.Args()
	}
	dir, err := r.dataDir()
	if err != nil {
		return nil, nil, err
	}
	if list {
		if len(pos) > 0 || session != "" || was != "" || wasNone || from != "" || date != "" || undo || made != "" {
			return nil, nil, usageError{"--list takes nothing else"}
		}
		return r.listRetags(dir)
	}
	if undo {
		t, err := time.Parse(time.RFC3339, made)
		if len(pos) > 0 || session == "" || err != nil || was != "" || wasNone || from != "" || date != "" {
			return nil, nil, usageError{"usage: sift retag --undo --session ID --at TIME, the time as sift retag --list --json gives it"}
		}
		if err := (evidence.Retags{Dir: dir}).Remove(session, t); err != nil {
			return nil, nil, err
		}
		return map[string]any{"removed": true}, func(out io.Writer) {
			fmt.Fprintf(out, "Took back session %s's retag of %s.\n", session, t.In(r.Now().Location()).Format("2006-01-02 15:04:05"))
		}, nil
	}
	if made != "" {
		return nil, nil, usageError{"--at names the retag --undo takes back; give --undo too"}
	}
	const use = "usage: sift retag KEY --session ID [--was KEY | --was-none] [--from HH:MM [--date YYYY-MM-DD]], or sift retag --list"
	switch {
	case len(pos) != 1 || !keyArg.MatchString(pos[0]) || session == "":
		return nil, nil, usageError{use}
	case was != "" && wasNone:
		return nil, nil, usageError{"--was and --was-none are two answers; pick one"}
	case was != "" && !keyArg.MatchString(was):
		return nil, nil, usageError{fmt.Sprintf("--was takes a key such as PUN-142, not %q", was)}
	case date != "" && from == "":
		return nil, nil, usageError{"--date is the day of --from; give --from too"}
	}
	key := strings.ToUpper(pos[0])
	now := r.Now()
	var at time.Time
	if from != "" {
		day := now
		if date != "" {
			if day, err = time.ParseInLocation("2006-01-02", date, now.Location()); err != nil {
				return nil, nil, usageError{fmt.Sprintf("--date takes YYYY-MM-DD, not %q", date)}
			}
		}
		g := clockRe.FindStringSubmatch(from)
		ok := g != nil
		if ok {
			at, ok = clockOn(day, g[1], g[2])
		}
		if !ok {
			return nil, nil, usageError{fmt.Sprintf("--from takes HH:MM, not %q", from)}
		}
	}
	var w *string
	switch {
	case was != "":
		k := strings.ToUpper(was)
		w = &k
	case !wasNone:
		w, _ = evidence.Sessions{Dir: dir}.Recall(session)
	}
	rt, saved, err := saveRetag(dir, session, w, key, at, now)
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{"saved": saved, "retag": rt}, func(out io.Writer) {
		if !saved {
			fmt.Fprintf(out, "Session %s already counts for %s; nothing to change.\n", session, key)
			return
		}
		since := ""
		if !at.IsZero() {
			since = " from " + at.Format("15:04") + " on"
		}
		fmt.Fprintf(out, "Session %s: its %s%s counts for %s.\n", session, timeOf(rt.Was), since, key)
	}, nil
}

func (r *run) listRetags(dir string) (any, func(io.Writer), error) {
	rs, problems, err := evidence.Retags{Dir: dir}.All()
	if err != nil {
		return nil, nil, err
	}
	for _, p := range problems {
		r.notes = append(r.notes, fmt.Sprintf("skipped %s line %d: %s", p.File, p.Line, p.Err))
	}
	return map[string]any{"retags": nonNil(rs)}, func(w io.Writer) {
		if len(rs) == 0 {
			fmt.Fprintln(w, "No retags.")
		}
		loc := r.Now().Location()
		for _, rt := range rs {
			was := rt.Was
			if was == "" {
				was = "no ticket"
			}
			since := ""
			if !rt.From.IsZero() {
				since = "  from " + rt.From.In(loc).Format("2006-01-02 15:04")
			}
			fmt.Fprintf(w, "%s  %s  %s → %s%s\n", rt.At.In(loc).Format("2006-01-02 15:04"), rt.Session, was, rt.Ticket, since)
		}
	}, nil
}
