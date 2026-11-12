package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"regexp"
)

// confirmYes matches a shell command that would run sift confirm --yes.
var confirmYes = regexp.MustCompile(`(^|[\s;&|(` + "`" + `])(\S*/)?sift\s+confirm\b[^;&|]*--yes`)

// GatesConfirm reports whether a shell command would confirm a day.
func GatesConfirm(command string) bool { return confirmYes.MatchString(command) }

type preToolUse struct {
	Tool  string `json:"tool_name"`
	Input struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// gate is the PreToolUse hook: an agent's command that would confirm a day
// is put to the user. It answers ask or nothing, never allow, so the
// user's own permission rules still apply, and it always exits 0.
func (r *run) gate(args []string) (any, func(io.Writer), error) {
	var hook bool
	if err := r.flags(args, func(fs *flag.FlagSet) { fs.BoolVar(&hook, "hook", false, "") }); err != nil {
		return nil, nil, err
	}
	r.json = false
	var in preToolUse
	if err := json.NewDecoder(r.Stdin).Decode(&in); err != nil {
		// cannot read the command: ask rather than let a confirm through
		in.Tool, in.Input.Command = "Bash", "sift confirm --yes"
	}
	if in.Tool != "Bash" || !GatesConfirm(in.Input.Command) {
		return nil, nil, nil
	}
	out := map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       "ask",
		"permissionDecisionReason": "Sift: this confirms a day and sends its worklogs. Check the draft first.",
	}}
	return nil, func(w io.Writer) { json.NewEncoder(w).Encode(out) }, nil
}

// nudge is the SessionStart hook: it tells the user when earlier days
// are waiting for review. Silent when none are, and always exits 0.
func (r *run) nudge() (any, func(io.Writer), error) {
	r.json = false
	w, err := r.waiting()
	if err != nil || len(w) == 0 {
		return nil, nil, nil
	}
	msg := fmt.Sprintf("Sift: %s waiting for review, from %s. Run sift to review.", plural(len(w), "day"), w[0])
	return nil, func(out io.Writer) { json.NewEncoder(out).Encode(map[string]string{"systemMessage": msg}) }, nil
}
