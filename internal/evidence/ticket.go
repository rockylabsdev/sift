package evidence

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// keyRe is a tracker key: a project of capitals and digits, then a number.
var keyRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]+-[0-9]+`)

// TicketFiles name a directory's ticket, nearest first. .punchcard is read
// so directories set up for Punchcard keep working.
var TicketFiles = []string{".sift", ".punchcard"}

// TicketFor tells dir's ticket: the nearest ticket file at dir or above
// (no higher than the git root) whose first meaningful line is a key, else
// the first key in the branch name. Nil when neither names one.
func TicketFor(dir string) (*string, *Found) {
	root := gitOut(dir, "rev-parse", "--show-toplevel")
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	for d := dir; ; d = filepath.Dir(d) {
		for _, name := range TicketFiles {
			path := filepath.Join(d, name)
			if k := firstKey(path); k != "" {
				return &k, &Found{Via: "file", Value: path}
			}
		}
		if d == root || d == filepath.Dir(d) {
			break
		}
	}
	if b := gitOut(dir, "symbolic-ref", "--short", "-q", "HEAD"); b != "" {
		if k := keyRe.FindString(b); k != "" {
			k = strings.ToUpper(k)
			return &k, &Found{Via: "branch", Value: b}
		}
	}
	return nil, nil
}

func firstKey(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if keyRe.MatchString(line) && keyRe.FindString(line) == line {
			return strings.ToUpper(line)
		}
		return ""
	}
	return ""
}

func gitOut(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
