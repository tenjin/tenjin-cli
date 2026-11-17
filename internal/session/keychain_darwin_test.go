//go:build darwin

package session

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// fakeSecurity stands in for /usr/bin/security: it saves what it reads to
// read, then waits for wait before it says, in landed, that it wrote.
func fakeSecurity(t *testing.T, wait string) (read, landed string) {
	t.Helper()
	dir := t.TempDir()
	read, landed = filepath.Join(dir, "read"), filepath.Join(dir, "landed")
	script := filepath.Join(dir, "security")
	body := "#!/bin/sh\ncat > '" + read + "'\nsleep " + wait + "\ntouch '" + landed + "'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	previous := securityPath
	securityPath = script
	t.Cleanup(func() { securityPath = previous })
	return read, landed
}

// The secret goes in on standard input, never among the arguments, encoded
// as go-keyring reads it back.
func TestKeychainSetWritesThroughSecurity(t *testing.T) {
	read, landed := fakeSecurity(t, "0")
	if err := keychainSet(context.Background(), keyringService, "production", `{"refresh_token":"it's"}`); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(read)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(`{"refresh_token":"it's"}`))
	if want := "add-generic-password -U -s 'tenjin-cli' -a 'production' -w 'go-keyring-base64:" + encoded + "'\n"; string(got) != want {
		t.Errorf("security read %q, want %q", got, want)
	}
	if _, err := os.Stat(landed); err != nil {
		t.Errorf("the write did not finish: %v", err)
	}
}

// A write still waiting when its context ends, as on a locked keychain's
// dialog, is killed: it never lands later.
func TestAKeychainWriteThatIsStoppedNeverLands(t *testing.T) {
	_, landed := fakeSecurity(t, "1")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := keychainSet(ctx, keyringService, "production", "secret"); err == nil {
		t.Fatal("a stopped write reported success")
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("stopping took %v", took)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(landed); !os.IsNotExist(err) {
		t.Errorf("the stopped write landed: %v", err)
	}
}

func TestSecurityWords(t *testing.T) {
	for in, want := range map[string]string{"": "''", "plain": "'plain'", "it's": `'it'"'"'s'`, `a "b" $c`: `'a "b" $c'`} {
		if got := securityWord(in); got != want {
			t.Errorf("securityWord(%q) = %s, want %s", in, got, want)
		}
	}
	if _, err := securityInput(keyringService, "production", strings.Repeat("x", 4096)); !errors.Is(err, keyring.ErrSetDataTooBig) {
		t.Errorf("a secret too long for security -i: %v", err)
	}
}
