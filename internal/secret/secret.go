// Package secret stores secrets by name, such as "jira-token:<email>" or
// "calendar-url" (Punchcard ADR 0006). On macOS a secret lives in the
// Keychain, reached only through /usr/bin/security. Elsewhere, or when the
// Keychain refuses a write, it is a 0600 file under
// $XDG_CONFIG_HOME/sift/secrets/. No error from this package carries a
// secret's value.
package secret

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrNotFound: no secret by that name.
var ErrNotFound = errors.New("no such secret")

// Source is where a secret was found or put.
type Source string

const (
	SourceKeychain Source = "keychain"
	SourceFile     Source = "file"
)

// Store is the secrets of one user.
type Store struct {
	// Dir holds one 0600 file per secret.
	Dir string
	// Keychain, when set (macOS), is tried before the file.
	Keychain *Keychain
}

// Open is the Store for this user on goos (runtime.GOOS outside tests).
func Open(getenv func(string) string, goos string) (Store, error) {
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		home := getenv("HOME")
		if home == "" {
			return Store{}, errors.New("neither XDG_CONFIG_HOME nor HOME is set, so sift has no config directory")
		}
		base = filepath.Join(home, ".config")
	}
	s := Store{Dir: filepath.Join(base, "sift", "secrets")}
	// A sandbox (scripts/sandbox.sh) keeps its secrets in its own files, so
	// trying a checkout never overwrites the real Keychain items.
	if goos == "darwin" && getenv("SIFT_SANDBOX") == "" {
		s.Keychain = &System
	}
	return s, nil
}

// A name becomes a file name and a Keychain account, so it is kept plain.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@:._+-]*$`)

func checkName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("%q is not a secret name", name)
	}
	return nil
}

// Get is the secret called name and where it was. The Keychain wins over a
// stray file. A Keychain that fails, with no file to fall back on, is that
// failure and not ErrNotFound.
func (s Store) Get(name string) (string, Source, error) {
	if err := checkName(name); err != nil {
		return "", "", err
	}
	var kcErr error
	if s.Keychain != nil {
		v, err := s.Keychain.Read(name)
		if err == nil {
			return v, SourceKeychain, nil
		}
		if !errors.Is(err, ErrNotFound) {
			kcErr = err
		}
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, name))
	switch {
	case err == nil:
		return strings.TrimSpace(string(b)), SourceFile, nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", "", err
	case kcErr != nil:
		return "", "", kcErr
	}
	return "", "", ErrNotFound
}

// Set stores value as name and says where it went. When the Keychain
// refuses it, the file holds it instead. A Keychain write removes any file
// copy, so there is one secret by each name.
func (s Store) Set(name, value string) (Source, error) {
	if err := checkName(name); err != nil {
		return "", err
	}
	if s.Keychain != nil && s.Keychain.Write(name, value) == nil {
		if err := os.Remove(filepath.Join(s.Dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return SourceKeychain, err
		}
		return SourceKeychain, nil
	}
	return SourceFile, s.writeFile(name, value)
}

// Delete removes name from everywhere. A name that is nowhere is not an
// error.
func (s Store) Delete(name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	if s.Keychain != nil {
		if err := s.Keychain.Delete(name); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	if err := os.Remove(filepath.Join(s.Dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s Store) writeFile(name, value string) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, "."+name+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.WriteString(value + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(s.Dir, name))
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("writing secret %s: %w", name, err)
	}
	return nil
}
