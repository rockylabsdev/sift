package secret

import (
	"bufio"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as a fake `security`, so the Keychain path runs
// on Linux CI. Items are files under $FAKE_SECURITY/<service>/<account>,
// and every argv is appended to $FAKE_SECURITY/argv.
func TestMain(m *testing.M) {
	if dir := os.Getenv("FAKE_SECURITY"); dir != "" {
		os.Exit(fakeSecurity(dir, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeSecurity(dir string, args []string) int {
	f, _ := os.OpenFile(filepath.Join(dir, "argv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	fmt.Fprintln(f, strings.Join(args, " "))
	f.Close()
	flag := func(args []string, name string) string {
		for i, a := range args {
			if a == name && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	item := func(args []string) string { return filepath.Join(dir, flag(args, "-s"), flag(args, "-a")) }
	switch args[0] {
	case "find-generic-password":
		b, err := os.ReadFile(item(args))
		if err != nil {
			return errSecItemNotFound
		}
		fmt.Println(string(b))
	case "delete-generic-password":
		if os.Remove(item(args)) != nil {
			return errSecItemNotFound
		}
	case "-i":
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if os.Getenv("FAKE_SECURITY_REFUSE") != "" {
			fmt.Fprintln(os.Stderr, "add-generic-password: refused, echoing "+line)
			return 0
		}
		var cmd []string
		for i, part := range strings.Split(strings.TrimSpace(line), `"`) {
			if i%2 == 1 {
				cmd = append(cmd, part)
			} else {
				cmd = append(cmd, strings.Fields(part)...)
			}
		}
		os.MkdirAll(filepath.Dir(item(cmd)), 0o700)
		os.WriteFile(item(cmd), []byte(flag(cmd, "-w")), 0o600)
	}
	return 0
}

func fakeKeychain(t *testing.T) (*Keychain, string) {
	dir := t.TempDir()
	t.Setenv("FAKE_SECURITY", dir)
	// The test binary starts slowly under -race, so it gets more than the
	// real binary's second.
	return &Keychain{Bin: os.Args[0], Service: "sift-test", Timeout: 20 * time.Second}, dir
}

const name, value = "jira-token:me@example.com", "ATATT3xFfGF0-a_b=c+d/e 1"

func TestFileStore(t *testing.T) {
	s := Store{Dir: filepath.Join(t.TempDir(), "secrets")}
	if _, _, err := s.Get(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("before Set: err = %v, want ErrNotFound", err)
	}
	if src, err := s.Set(name, value); err != nil || src != SourceFile {
		t.Fatalf("Set = %q, %v", src, err)
	}
	if v, src, err := s.Get(name); v != value || src != SourceFile || err != nil {
		t.Fatalf("Get = %q, %q, %v", v, src, err)
	}
	fi, err := os.Stat(filepath.Join(s.Dir, name))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
	if err := s.Delete(name); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(name); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
	if _, _, err := s.Get(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Delete: err = %v", err)
	}
}

func TestNamesStayPlain(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, bad := range []string{"", "../config.json", "a/b", ".hidden"} {
		if _, err := s.Set(bad, "x"); err == nil {
			t.Errorf("Set(%q) took a name that could escape the directory", bad)
		}
	}
}

func TestKeychainWinsAndValueNeverInArgv(t *testing.T) {
	kc, fake := fakeKeychain(t)
	s := Store{Dir: t.TempDir(), Keychain: kc}
	os.WriteFile(filepath.Join(s.Dir, name), []byte("stale\n"), 0o600)

	if src, err := s.Set(name, value); err != nil || src != SourceKeychain {
		t.Fatalf("Set = %q, %v", src, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, name)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a Keychain write left the file copy: %v", err)
	}
	if v, src, err := s.Get(name); v != value || src != SourceKeychain || err != nil {
		t.Fatalf("Get = %q, %q, %v", v, src, err)
	}
	argv, _ := os.ReadFile(filepath.Join(fake, "argv"))
	if strings.Contains(string(argv), value) {
		t.Errorf("the secret reached argv:\n%s", argv)
	}
	if err := s.Delete(name); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Delete: err = %v", err)
	}
}

func TestRefusedKeychainFallsBackToFile(t *testing.T) {
	kc, _ := fakeKeychain(t)
	t.Setenv("FAKE_SECURITY_REFUSE", "1")
	s := Store{Dir: t.TempDir(), Keychain: kc}
	if src, err := s.Set(name, value); err != nil || src != SourceFile {
		t.Fatalf("Set = %q, %v; want the file", src, err)
	}
	if v, src, err := s.Get(name); v != value || src != SourceFile || err != nil {
		t.Fatalf("Get = %q, %q, %v", v, src, err)
	}
	err := kc.Write(name, value)
	if err == nil || strings.Contains(err.Error(), value) {
		t.Errorf("refused write: err = %v; want an error that hides the secret", err)
	}
}

func TestBrokenKeychainIsNotNotFound(t *testing.T) {
	s := Store{Dir: t.TempDir(), Keychain: &Keychain{Bin: filepath.Join(t.TempDir(), "no-security")}}
	if _, _, err := s.Get(name); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v; want the Keychain's failure", err)
	}
}

// TestKeychainLive round-trips a throwaway item through the real
// /usr/bin/security. Set SIFT_LIVE_KEYCHAIN=1 on a Mac to run it.
func TestKeychainLive(t *testing.T) {
	if os.Getenv("SIFT_LIVE_KEYCHAIN") != "1" {
		t.Skip("set SIFT_LIVE_KEYCHAIN=1 to touch a throwaway Keychain item")
	}
	kc := Keychain{Bin: System.Bin, Service: fmt.Sprintf("sift-test-%d", rand.Int64())}
	t.Cleanup(func() { kc.Delete(name) })
	if err := kc.Write(name, value); err != nil {
		t.Fatal(err)
	}
	if err := kc.Write(name, value+"2"); err != nil {
		t.Fatal(err)
	}
	if v, err := kc.Read(name); v != value+"2" || err != nil {
		t.Fatalf("Read = %q, %v", v, err)
	}
	if err := kc.Delete(name); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.Read(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Delete: err = %v", err)
	}
}
