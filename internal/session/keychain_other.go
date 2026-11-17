//go:build !darwin

package session

import "context"

// keychainSet writes to the system keychain through go-keyring. Here the
// call cannot be stopped once made (a D-Bus call to the Secret Service, the
// Windows Credential Manager): the store keeps the lock until it returns.
func keychainSet(ctx context.Context, service, user, secret string) error {
	return keyringSet(ctx, service, user, secret)
}
