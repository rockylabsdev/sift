package secret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Keychain is the macOS Keychain reached only through Apple's signed
// `security` binary. An item that binary writes, it reads back for any
// caller with no prompt, even from a hook with no TTY. A secret never goes
// in argv: it comes back on stdout and is written over stdin.
type Keychain struct {
	Bin     string
	Service string        // the item's service; the secret's name is its account
	Timeout time.Duration // per call; zero is one second
}

// System is the real Keychain. Only a hand check may touch it.
var System = Keychain{Bin: "/usr/bin/security", Service: "sift"}

// ErrTimeout: the Keychain did not answer in time.
var ErrTimeout = errors.New("the Keychain did not answer in time")

// errSecItemNotFound is security's exit status for "no such item".
const errSecItemNotFound = 44

// Read is the secret stored as name, or ErrNotFound.
func (k Keychain) Read(name string) (string, error) {
	out, err := k.run(nil, "", "find-generic-password", "-s", k.Service, "-a", name, "-w")
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(out, "\n"), nil
}

// Write stores value as name, replacing an item of the same name. The
// command goes over stdin to `security -i`.
func (k Keychain) Write(name, value string) error {
	if strings.ContainsAny(name+value, "\"\\\n\r") {
		return errors.New("the secret has a character the Keychain command cannot carry")
	}
	line := fmt.Sprintf("add-generic-password -U -s %q -a %q -w %q\n", k.Service, name, value)
	out, err := k.run(strings.NewReader(line), value, "-i")
	if err != nil {
		return err
	}
	// security -i exits 0 even when the command inside it failed. It says
	// so on stderr.
	if msg := strings.TrimSpace(out); msg != "" {
		return fmt.Errorf("security add-generic-password: %s", msg)
	}
	return nil
}

// Delete removes the item called name, or answers ErrNotFound.
func (k Keychain) Delete(name string) error {
	_, err := k.run(nil, "", "delete-generic-password", "-s", k.Service, "-a", name)
	return err
}

// run calls security with args, which never hold a secret. For `-i` it
// returns stdout and stderr, else stdout. secret, when set, is scrubbed from any error
// text in case security echoes its input.
func (k Keychain) run(stdin *strings.Reader, secret string, args ...string) (string, error) {
	d := k.Timeout
	if d == 0 {
		d = time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cmd := exec.CommandContext(ctx, k.Bin, args...)
	cmd.WaitDelay = 100 * time.Millisecond
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if stdin != nil {
		cmd.Stdin = stdin
	}
	err := cmd.Run()
	scrub := func(s string) string {
		s = strings.TrimSpace(s)
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "<secret>")
		}
		return s
	}
	if ctx.Err() != nil {
		return "", ErrTimeout
	}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit) && exit.ExitCode() == errSecItemNotFound:
		return "", ErrNotFound
	case errors.As(err, &exit):
		msg := scrub(stderr.String())
		if msg == "" {
			msg = "exit status " + strconv.Itoa(exit.ExitCode())
		}
		return "", fmt.Errorf("security %s: %s", args[0], msg)
	case err != nil:
		return "", err
	}
	if args[0] == "-i" {
		return scrub(stdout.String() + stderr.String()), nil
	}
	return stdout.String(), nil
}
