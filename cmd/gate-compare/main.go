// Command gate-compare is scaffolding for slice 1's gate: a week of real
// use, comparing each day's sift draft with the worklogs punchcard's timer
// logged. Delete it once the gate is passed.
//
//	go run ./cmd/gate-compare --from 2026-11-16 --to 2026-11-20 --idle 5,10,15
//
// It runs `punchcard log --json` once and `sift draft --json` per day and
// idle gap (each with its own config directory), and prints a table per
// day and a summary: how many ticket-days land within a quarter hour,
// which the spec names as the estimate's expected accuracy.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type siftDraft struct {
	Data struct {
		Worklogs []struct {
			Ticket  string `json:"ticket"`
			Minutes int    `json:"minutes"`
		} `json:"worklogs"`
		Unassigned struct{ Meetings, Gaps, Ticketless int } `json:"unassigned"`
	} `json:"data"`
}

type timerLog struct {
	Data struct {
		Rows []struct {
			Date    string `json:"date"`
			Key     string `json:"key"`
			Minutes int    `json:"minutes"`
		} `json:"rows"`
	} `json:"data"`
}

// day is one date's minutes per ticket: the timer's, and sift's per idle gap.
type day struct {
	date       string
	timer      map[string]int
	sift       map[int]map[string]int
	unassigned map[int]int
}

func main() {
	from := flag.String("from", "", "first date, YYYY-MM-DD (default: Monday of this week)")
	to := flag.String("to", "", "last date, YYYY-MM-DD (default: today)")
	idle := flag.String("idle", "10", "idle gaps to derive with, comma separated minutes")
	flag.Parse()
	gaps, err := parseGaps(*idle)
	if err != nil {
		fail(err)
	}
	now := time.Now()
	if *to == "" {
		*to = now.Format("2006-01-02")
	}
	if *from == "" {
		monday := now.AddDate(0, 0, -((int(now.Weekday()) + 6) % 7))
		*from = monday.Format("2006-01-02")
	}
	days, err := collect(*from, *to, gaps, run)
	if err != nil {
		fail(err)
	}
	report(os.Stdout, days, gaps)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "gate-compare:", err)
	os.Exit(1)
}

func parseGaps(s string) ([]int, error) {
	var out []int
	for _, f := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 1 || n > 120 {
			return nil, fmt.Errorf("--idle takes minutes from 1 to 120, not %q", f)
		}
		out = append(out, n)
	}
	return out, nil
}

// runner runs a command with extra environment and returns its stdout.
type runner func(env []string, name string, args ...string) ([]byte, error)

func run(env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "CLAUDECODE=", "AI_AGENT=")
	cmd.Env = append(cmd.Env, env...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if _, isExit := err.(*exec.ExitError); isExit && len(out) > 0 {
		err = nil // the envelope says what went wrong
	}
	return out, err
}

func collect(from, to string, gaps []int, run runner) ([]day, error) {
	start, err := time.ParseInLocation("2006-01-02", from, time.Local)
	if err != nil {
		return nil, err
	}
	end, err := time.ParseInLocation("2006-01-02", to, time.Local)
	if err != nil {
		return nil, err
	}
	out, err := run(nil, "punchcard", "log", "--json", "--from", from, "--to", to)
	if err != nil {
		return nil, fmt.Errorf("punchcard log: %v", err)
	}
	var tl timerLog
	if err := json.Unmarshal(out, &tl); err != nil {
		return nil, fmt.Errorf("punchcard log: %v", err)
	}
	cfg, err := os.MkdirTemp("", "gate-compare")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(cfg)

	var days []day
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		dd := day{date: d.Format("2006-01-02"), timer: map[string]int{}, sift: map[int]map[string]int{}, unassigned: map[int]int{}}
		for _, r := range tl.Data.Rows {
			if r.Date == dd.date {
				dd.timer[r.Key] += r.Minutes
			}
		}
		for _, g := range gaps {
			dir := filepath.Join(cfg, strconv.Itoa(g))
			os.MkdirAll(filepath.Join(dir, "sift"), 0o700)
			os.WriteFile(filepath.Join(dir, "sift", "config.json"), []byte(fmt.Sprintf(`{"idle_gap_minutes":%d}`, g)), 0o600)
			out, err := run([]string{"XDG_CONFIG_HOME=" + dir}, "sift", "draft", "--json", "--date", dd.date)
			if err != nil {
				return nil, fmt.Errorf("sift draft --date %s: %v", dd.date, err)
			}
			var sd siftDraft
			if err := json.Unmarshal(out, &sd); err != nil {
				return nil, fmt.Errorf("sift draft --date %s: %v", dd.date, err)
			}
			dd.sift[g] = map[string]int{}
			for _, w := range sd.Data.Worklogs {
				dd.sift[g][w.Ticket] = w.Minutes
			}
			u := sd.Data.Unassigned
			dd.unassigned[g] = u.Meetings + u.Gaps + u.Ticketless
		}
		days = append(days, dd)
	}
	return days, nil
}

func dur(n int) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	if n < 60 {
		return fmt.Sprintf("%s%dm", sign, n)
	}
	return fmt.Sprintf("%s%dh %02dm", sign, n/60, n%60)
}

// fit is how a set of ticket-days compares with the timer.
type fit struct{ ticketDays, within, absDiff int }

func report(w io.Writer, days []day, gaps []int) {
	fits := map[int]*fit{}
	for _, g := range gaps {
		fits[g] = &fit{}
	}
	for _, d := range days {
		keys := map[string]bool{}
		for k := range d.timer {
			keys[k] = true
		}
		for _, s := range d.sift {
			for k := range s {
				keys[k] = true
			}
		}
		if len(keys) == 0 {
			continue
		}
		var ks []string
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		day, _ := time.Parse("2006-01-02", d.date)
		fmt.Fprintf(w, "%s\n  %-10s %8s", day.Format("Mon 2 Jan"), "ticket", "timer")
		for _, g := range gaps {
			fmt.Fprintf(w, " %15s", fmt.Sprintf("sift@%dm", g))
		}
		fmt.Fprintln(w)
		tt := 0
		ts := map[int]int{}
		for _, k := range ks {
			fmt.Fprintf(w, "  %-10s %8s", k, dur(d.timer[k]))
			tt += d.timer[k]
			for _, g := range gaps {
				s := d.sift[g][k]
				ts[g] += s
				diff := s - d.timer[k]
				f := fits[g]
				f.ticketDays++
				f.absDiff += max(diff, -diff)
				if max(diff, -diff) <= 15 {
					f.within++
				}
				fmt.Fprintf(w, " %15s", fmt.Sprintf("%s %s", dur(s), "("+sign(diff)+")"))
			}
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "  %-10s %8s", "total", dur(tt))
		for _, g := range gaps {
			fmt.Fprintf(w, " %15s", dur(ts[g]))
		}
		fmt.Fprintf(w, "\n  %-10s %8s", "unassigned", "")
		for _, g := range gaps {
			fmt.Fprintf(w, " %15s", dur(d.unassigned[g]))
		}
		fmt.Fprint(w, "\n\n")
	}
	fmt.Fprintln(w, "summary: ticket-days within 15m of the timer")
	for _, g := range gaps {
		f := fits[g]
		if f.ticketDays == 0 {
			fmt.Fprintf(w, "  idle gap %2dm: no ticket-days\n", g)
			continue
		}
		fmt.Fprintf(w, "  idle gap %2dm: %d of %d (%d%%), mean difference %s\n", g, f.within, f.ticketDays, 100*f.within/f.ticketDays, dur(f.absDiff/f.ticketDays))
	}
}

func sign(n int) string {
	if n > 0 {
		return "+" + dur(n)
	}
	if n == 0 {
		return "="
	}
	return dur(n)
}
