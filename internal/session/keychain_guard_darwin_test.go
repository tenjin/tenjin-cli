//go:build darwin && !keychain

package session

// The tests never reach the real keychain through keychainSet: a store that
// would is built by NewStore alone, and only the opt-in keychain test, run
// with -tags keychain, writes to the system keychain.
func init() { securityPath = "" }
