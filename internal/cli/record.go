package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/rockylabsdev/sift/internal/evidence"
)

// hookInput is the part of a Claude Code hook's input sift reads. A
// subagent's hooks carry the session of the conversation that started it,
// its own agent_id, and its worktree as cwd.
type hookInput struct {
	Cwd        string `json:"cwd"`
	Event      string `json:"hook_event_name"`
	Session    string `json:"session_id"`
	AgentID    string `json:"agent_id"`
	DurationMs int64  `json:"duration_ms"`
	Prompt     string `json:"prompt"`
}

// agentKinds maps the hooks that report an agent's activity to the event
// kind they record.
var agentKinds = map[string]string{
	"PostToolUse":   "tool",
	"SubagentStart": "subagent-start",
	"SubagentStop":  "subagent-stop",
	"Stop":          "turn-end",
}

// origin infers who is acting: an agent's shell sets CLAUDECODE or AI_AGENT.
func (r *run) origin() evidence.Origin {
	if r.Getenv("CLAUDECODE") != "" || r.Getenv("AI_AGENT") != "" {
		return evidence.Agent
	}
	return evidence.Hand
}

// record appends one event. With --hook it reads Claude Code's hook input
// from stdin, records the user's prompt, and always exits 0: a failed
// record must never stop the user typing. Its stdout is added to the
// prompt, so it stays empty except to answer /sift retag.
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
		reply, err := r.recordHook(in)
		if err != nil {
			fmt.Fprintln(r.Stderr, "sift:", err)
		}
		if reply == "" {
			return nil, nil, nil
		}
		out := map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "UserPromptSubmit", "additionalContext": reply}}
		return nil, func(w io.Writer) { json.NewEncoder(w).Encode(out) }, nil
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
		r.notes = append(r.notes, "not recorded: an agent's activity is recorded by the plugin's hooks, not by sift record")
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
		e.Ticket, e.Found = evidence.TicketFor(cwd, r.projects())
	}
	return e, evidence.Log{Dir: dir}.Append(e)
}

// projects is the configured project keys; a broken config must not lose
// evidence, so it is reported and the unfiltered rule used.
func (r *run) projects() []string {
	cfg, err := r.config()
	if err != nil {
		fmt.Fprintln(r.Stderr, "sift: ignoring projects:", err)
	}
	return cfg.Projects
}

// recordHook records one Claude Code hook: the user's prompt, or an
// agent's activity. An agent's ticket is its directory's own (a .sift
// file, or a branch that names a key), else the ticket of the user's
// latest prompt in the same session: subagents run in worktrees whose
// branches rarely name one. A /sift retag prompt is recorded too, and its
// reply is the context to give Claude.
func (r *run) recordHook(in hookInput) (string, error) {
	dir, err := r.dataDir()
	if err != nil {
		return "", err
	}
	sessions := evidence.Sessions{Dir: dir}
	now := r.Now()
	e := evidence.Event{At: now, Source: "claude-code", Dir: in.Cwd, Session: in.Session}
	if in.Cwd != "" {
		e.Ticket, e.Found = evidence.TicketFor(in.Cwd, r.projects())
	}
	kind, isAgent := agentKinds[in.Event]
	if !isAgent {
		e.Kind, e.Origin = "prompt", evidence.Prompt
		if in.Session != "" {
			if err := sessions.Remember(in.Session, e.Ticket, e.Found); err != nil {
				fmt.Fprintln(r.Stderr, "sift:", err)
			}
		}
		if err := (evidence.Log{Dir: dir}).Append(e); err != nil {
			return "", err
		}
		return r.retagPrompt(dir, in, e), nil
	}
	e.Kind, e.Origin, e.AgentID = kind, evidence.Agent, in.AgentID
	if in.Event == "PostToolUse" && in.DurationMs > 0 {
		e.SpanMs = in.DurationMs
	}
	if e.Ticket == nil && in.Session != "" {
		e.Ticket, e.Found = sessions.Recall(in.Session)
	}
	t := ""
	if e.Ticket != nil {
		t = *e.Ticket
	}
	// at most one a minute per session and ticket, but never drop a long
	// run: its span is time the deriver counts
	if e.SpanMs < 60_000 && !sessions.Due(in.Session, t, now) {
		return "", nil
	}
	return "", evidence.Log{Dir: dir}.Append(e)
}
