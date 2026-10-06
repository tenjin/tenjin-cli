//go:build keychain

package session

import (
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// This test writes to the real system keychain, so it only runs on request:
//
//	go test -tags keychain ./internal/session
func TestSystemStoreRoundTripsThroughTheKeychain(t *testing.T) {
	const environment = "selftest"
	t.Cleanup(func() { _ = keyring.Delete(keyringService, environment) })

	store := &systemStore{dir: t.TempDir()}
	if got, err := store.Load(environment); err != nil || got != nil {
		t.Fatalf("before saving: got %+v, err %v", got, err)
	}

	want := &Credentials{
		Environment:      environment,
		ClientID:         "client",
		RefreshToken:     "refresh",
		JWT:              "header.payload.signature",
		JWTExpiry:        time.Now().Add(time.Hour).Truncate(time.Second),
		OrganizationName: "Acme Games",
	}
	location, err := store.Save(want)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("saved in %s", location)

	got, err := store.Load(environment)
	if err != nil || got == nil {
		t.Fatalf("load: %+v, err %v", got, err)
	}
	if got.RefreshToken != want.RefreshToken || got.JWT != want.JWT || !got.JWTExpiry.Equal(want.JWTExpiry) || got.OrganizationName != want.OrganizationName {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
