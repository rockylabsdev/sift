package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/rockylabsdev/sift/internal/evidence"
)

// hookInput is the part of Claude Code's UserPromptSubmit input sift reads.
type hookInput struct {
	Cwd   string `json:"cwd"`
	Event string `json:"hook_event_name"`
}

// origin infers who is acting: an agent's shell sets CLAUDECODE or AI_AGENT.
func (r *run) origin() evidence.Origin {
	if r.Getenv("CLAUDECODE") != "" || r.Getenv("AI_AGENT") != "" {
		return evidence.Agent
	}
	return evidence.Hand
}

// record appends one event. With --hook it reads Claude Code's hook input
// from stdin, records the user's prompt, and always exits 0 with nothing
// on stdout: a hook's stdout would be added to the prompt, and a failed
// record must never stop the user typing.
func (r *run) record(args []string) (any, func(io.Writer), error) {
	var cwd, source, kind, origin string
	var hook bool
	err := r.flags(args, func(fs *flag.FlagSet) {
		fs.StringVar(&cwd, "cwd", "", "")
		fs.StringVar(&source, "source", "cli", "")
		fs.StringVar(&kind, "kind", "hand", "")
		fs.StringVar(&origin, "origin", "", "")
		fs.BoolVar(&hook, "hook", false, "")
	})
	if hook {
		r.json = false
		if err != nil {
			fmt.Fprintln(r.Stderr, "sift:", err)
			return nil, nil, nil
		}
		var in hookInput
		if err := json.NewDecoder(r.Stdin).Decode(&in); err != nil {
			fmt.Fprintln(r.Stderr, "sift: reading the hook input:", err)
			return nil, nil, nil
		}
		if _, err := r.append(in.Cwd, "claude-code", "prompt", evidence.Prompt); err != nil {
			fmt.Fprintln(r.Stderr, "sift:", err)
		}
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if cwd == "" {
		cwd = r.Getenv("PWD")
	}
	o := r.origin()
	if origin != "" {
		o = evidence.Origin(origin)
		if o != evidence.Hand && o != evidence.Prompt && o != evidence.Agent {
			return nil, nil, usageError{fmt.Sprintf("--origin is hand, prompt or agent, not %q", origin)}
		}
	}
	if !o.IsEvidence() {
		r.notes = append(r.notes, "not recorded: an agent's own activity is not evidence")
		return map[string]any{"recorded": false, "origin": o}, nil, nil
	}
	e, err := r.append(cwd, source, kind, o)
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{"recorded": true, "event": e}, func(w io.Writer) {
		t := "no ticket"
		if e.Ticket != nil {
			t = *e.Ticket + " (" + e.Found.Via + ")"
		}
		fmt.Fprintf(w, "Recorded %s · %s\n", e.At.Local().Format("15:04"), t)
	}, nil
}

func (r *run) append(cwd, source, kind string, o evidence.Origin) (evidence.Event, error) {
	dir, err := r.dataDir()
	if err != nil {
		return evidence.Event{}, err
	}
	e := evidence.Event{At: r.Now(), Source: source, Kind: kind, Dir: cwd, Origin: o}
	if cwd != "" {
		cfg, err := r.config()
		if err != nil { // a broken config must not lose evidence
			fmt.Fprintln(r.Stderr, "sift: ignoring projects:", err)
		}
		e.Ticket, e.Found = evidence.TicketFor(cwd, cfg.Projects)
	}
	return e, evidence.Log{Dir: dir}.Append(e)
}
