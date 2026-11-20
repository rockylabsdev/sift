package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Source is where a Day's meetings came from.
type Source string

const (
	Fetched Source = "fetched"
	Cached  Source = "cached"
	None    Source = "none" // no calendar, or it failed with nothing cached
)

// Day is one day of the calendar as a draft needs it.
type Day struct {
	Meetings []Meeting `json:"meetings"`
	Skipped  []Skipped `json:"skipped"`
	Source   Source    `json:"-"`
	// At is when the meetings were fetched; zero when Source is None.
	At time.Time `json:"at"`
	// Problem says, for the user, why the meetings are stale or missing.
	Problem string `json:"-"`
}

// Timeout bounds one fetch of the feed. Outlook publishes a calendar's
// whole history in one file, which can take several seconds to arrive.
const Timeout = 30 * time.Second

// most bounds the feed's size; years of history stay well under it.
const most = 64 << 20

// Load fetches the feed at address and reads day from it, saving the
// result under dataDir. When the fetch or the feed fails, it falls back
// to the saved day. An empty address means no calendar and no problem.
// address is a secret, so no Problem names it.
func Load(ctx context.Context, client *http.Client, dataDir, address, owner string, day time.Time) Day {
	if address == "" {
		return Day{Source: None}
	}
	cache := filepath.Join(dataDir, "calendar", day.Format("2006-01-02")+".json")
	ics, err := fetch(ctx, client, address)
	var d Day
	if err == nil {
		d.Meetings, d.Skipped, err = Parse(ics, owner, day)
	}
	if err != nil {
		return fallback(cache, err.Error(), day.Location())
	}
	d.Source, d.At = Fetched, time.Now()
	if err := save(cache, d); err != nil {
		d.Problem = "The calendar was read but could not be saved for later: " + err.Error()
	}
	return d
}

func fallback(cache, why string, loc *time.Location) Day {
	var d Day
	b, err := os.ReadFile(cache)
	if err == nil {
		err = json.Unmarshal(b, &d)
	}
	if err != nil {
		return Day{Source: None, Problem: "Meetings are missing: " + why + "."}
	}
	d.Source = Cached
	d.Problem = fmt.Sprintf("Meetings are as of %s: %s.", d.At.In(loc).Format("Jan 2 15:04"), why)
	return d
}

func save(path string, d Day) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".day-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// fetch returns the feed's body. Its errors never contain the address.
func fetch(ctx context.Context, client *http.Client, address string) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if rest, ok := cutPrefixFold(address, "webcal://"); ok {
		address = "https://" + rest
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	slow := tooSlow(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("the calendar address is not a valid URL")
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, slow
		case errors.As(err, &ue):
			return nil, fmt.Errorf("could not reach the calendar (%s)", scrub(ue.Err.Error(), address))
		default:
			return nil, errors.New("could not reach the calendar")
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the calendar answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, most+1))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, slow
		}
		return nil, errors.New("the calendar stopped answering partway")
	}
	if len(b) > most {
		return nil, errors.New("the calendar is too large to read")
	}
	return b, nil
}

// tooSlow is the error for a fetch that runs past ctx's deadline.
func tooSlow(ctx context.Context) error {
	deadline, _ := ctx.Deadline()
	return fmt.Errorf("the calendar took longer than %s to arrive", time.Until(deadline).Round(time.Second))
}

// scrub removes the address from a transport error, which can quote it
// when a redirect fails.
func scrub(msg, address string) string {
	if u, err := url.Parse(address); err == nil {
		for _, s := range []string{address, u.Path, u.RawQuery} {
			if s != "" && s != "/" {
				msg = strings.ReplaceAll(msg, s, "…")
			}
		}
	}
	return msg
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return "", false
}
