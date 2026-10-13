package config

import (
	"strings"
	"testing"
)

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestResolve(t *testing.T) {
	e, err := Resolve(env(nil))
	if err != nil || e.Name != "production" || e.API != "https://api.tenjin.com/v2" || e.Track != "https://track.tenjin.com" {
		t.Errorf("default: %+v, %v", e, err)
	}
	if _, err := Resolve(env(map[string]string{"TENJIN_API_URL": "ftp://example.com"})); err == nil {
		t.Error("an address that is not http or https was accepted")
	}
}

// A substituted address must not be handed the session of the environment
// it replaces. The session store is keyed by the environment's name.
func TestAnOverriddenAddressGetsItsOwnSession(t *testing.T) {
	e, err := Resolve(env(map[string]string{"TENJIN_TRACK_URL": "http://127.0.0.1:9999/"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.Name, "custom-") || e.Track != "http://127.0.0.1:9999" {
		t.Errorf("endpoints = %+v", e)
	}
	// The addresses that were not overridden stay as they were.
	if e.API != "https://api.tenjin.com/v2" || e.Issuer != "https://dashboard.tenjin.com" {
		t.Errorf("endpoints = %+v", e)
	}
	if got := e.SDKKeysURL("abc"); got != "https://dashboard.tenjin.com/dashboard/apps/abc/sdk" {
		t.Errorf("SDK keys page = %q", got)
	}

	for _, bad := range []string{"track.example.com", "ftp://example.com", "http://", "http://api.example.com", "http://10.0.0.1:8080"} {
		if _, err := Resolve(env(map[string]string{"TENJIN_API_URL": bad, "TENJIN_DASHBOARD_URL": "http://127.0.0.1:1"})); err == nil || !strings.Contains(err.Error(), "TENJIN_API_URL") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

// Each set of addresses keeps its own session, and production's dashboard
// never signs in for an API that was replaced.
func TestEachSetOfAddressesHasItsOwnSession(t *testing.T) {
	a, errA := Resolve(env(map[string]string{"TENJIN_DASHBOARD_URL": "http://127.0.0.1:1", "TENJIN_API_URL": "http://127.0.0.1:2"}))
	b, errB := Resolve(env(map[string]string{"TENJIN_DASHBOARD_URL": "http://127.0.0.1:1", "TENJIN_API_URL": "http://127.0.0.1:3"}))
	if errA != nil || errB != nil || a.Name == b.Name {
		t.Errorf("%q and %q (%v, %v)", a.Name, b.Name, errA, errB)
	}
	if _, err := Resolve(env(map[string]string{"TENJIN_API_URL": "http://127.0.0.1:2"})); err == nil || !strings.Contains(err.Error(), "TENJIN_DASHBOARD_URL") {
		t.Errorf("err = %v", err)
	}
}

// A token or an SDK key goes to these addresses, so plain http is accepted
// only where nobody else on the network can read it.
func TestPlainHTTPOnlyOnThisMachine(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:3999", "http://localhost:3000", "http://[::1]:8080", "http://tracker.localhost", "https://api.example.com/v2"} {
		if _, err := Resolve(env(map[string]string{"TENJIN_DASHBOARD_URL": "https://dashboard.example.com", "TENJIN_API_URL": ok})); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	_, err := Resolve(env(map[string]string{"TENJIN_TRACK_URL": "http://track.example.com"}))
	if err == nil || !strings.Contains(err.Error(), "must be an https address") {
		t.Errorf("plain http tracking address: err = %v", err)
	}
}
