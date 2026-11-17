//go:build darwin

package session

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"

	"github.com/tenjin/tenjin-cli/internal/proc"
)

// securityPath is macOS's keychain tool.
var securityPath = "/usr/bin/security"

// keychainSet writes to the macOS keychain through security, as go-keyring
// does, but as a program of the CLI's own (internal/proc): when ctx ends, a
// write still waiting, as on a locked keychain's password dialog, is killed.
// It can then never land after the lock is let go, nor after this process
// has exited, over a sign-in another tenjin saved since.
func keychainSet(ctx context.Context, service, user, secret string) error {
	input, err := securityInput(service, user, secret)
	if err != nil {
		return err
	}
	cmd := proc.Command(ctx, securityPath, "-i")
	cmd.Stdin = strings.NewReader(input)
	return cmd.Run()
}

// securityInput is the line security -i reads to write secret. The password
// goes in on standard input, so that no other process sees it among this
// one's arguments, and is encoded as go-keyring encodes it, so that
// keyring.Get reads it back.
func securityInput(service, user, secret string) (string, error) {
	password := "go-keyring-base64:" + base64.StdEncoding.EncodeToString([]byte(secret))
	line := fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n", securityWord(service), securityWord(user), securityWord(password))
	// security -i reads a line of at most this many bytes.
	if len(line) > 4096 {
		return "", keyring.ErrSetDataTooBig
	}
	return line, nil
}

// securityWord quotes s as one word for security -i, which splits a line as
// a POSIX shell does.
func securityWord(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
